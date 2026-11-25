// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

// Package reconciler runs krytond's background loops. TTL periodically
// lists machines across all configured projects through a
// provider.Provider, deletes those past their expiry, and emits an event
// on the shared events.Bus for each expiry.
package reconciler

import (
	"context"
	"log/slog"
	"time"

	"github.com/zyvorai/kryton/internal/audit"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

// ExpiryOverrides lets operators extend or disable a machine's TTL after
// creation (schedule.Store implements it). Expiry returns ok=false when
// there is no override, and a nil time for "never expires".
type ExpiryOverrides interface {
	Expiry(project, machineID string) (*time.Time, bool)
	ForgetMachine(project, machineID string) int
}

// DeleteGuard vetoes the deletion of protected machines
// (protect.Store implements it).
type DeleteGuard interface {
	BeginDelete(project, machineID string) (release func(), err error)
}

// TTL periodically expires machines whose Spec.TTLMinutes deadline has
// passed. Zero-value Interval defaults to 30s in Run.
type TTL struct {
	Provider provider.Provider
	Projects []string
	Events   *events.Bus
	Log      *slog.Logger
	Interval time.Duration
	// Overrides, when set, takes precedence over a machine's own ExpiresAt.
	Overrides ExpiryOverrides
	// Guard, when set, keeps protected machines past their expiry.
	Guard DeleteGuard
	// Audit, when set, records every automatic deletion.
	Audit *audit.Store
	// WarnBefore, when > 0, publishes io.kryton.machine.expiring once per
	// machine when it is within that window of its expiry.
	WarnBefore time.Duration
	// OnExpire is called after each automatic deletion (metrics hook).
	OnExpire func(project string)
	Now      func() time.Time

	warned map[string]bool
	// held remembers machines whose expiry was already reported as blocked.
	held map[string]bool
}

// Run sweeps every configured project once immediately, then on every
// tick of Interval, until ctx is canceled. Intended to run for the
// lifetime of the krytond process in its own goroutine; blocks until
// ctx.Done().
func (r TTL) Run(ctx context.Context) {
	if r.Interval <= 0 {
		r.Interval = 30 * time.Second
	}
	if r.warned == nil {
		r.warned = map[string]bool{}
	}
	if r.held == nil {
		r.held = map[string]bool{}
	}
	ticker := time.NewTicker(r.Interval)
	defer ticker.Stop()
	r.runOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.runOnce(ctx)
		}
	}
}

func (r TTL) runOnce(ctx context.Context) {
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now()
	}
	for _, project := range r.Projects {
		machines, err := r.Provider.List(ctx, project)
		if err != nil {
			r.Log.Warn("ttl reconcile list failed", "project", project, "error", err)
			continue
		}
		for _, m := range machines {
			expires := m.ExpiresAt
			if r.Overrides != nil {
				if at, ok := r.Overrides.Expiry(project, m.ID); ok {
					expires = at
				}
			}
			if expires == nil {
				continue
			}
			if now.Before(*expires) {
				delete(r.held, m.ID)
				if r.WarnBefore > 0 && r.warned != nil && expires.Sub(now) <= r.WarnBefore && !r.warned[m.ID] {
					r.warned[m.ID] = true
					r.Events.Publish(ctx, "io.kryton.machine.expiring", "machines/"+m.ID, map[string]any{"project": project, "machineId": m.ID, "name": m.Spec.Name, "expiresAt": expires.UTC().Format(time.RFC3339)})
				}
				continue
			}
			if !r.expire(ctx, project, m) {
				continue
			}
			if r.Overrides != nil {
				r.Overrides.ForgetMachine(project, m.ID)
			}
			delete(r.warned, m.ID)
			delete(r.held, m.ID)
			if r.Audit != nil {
				_, _ = r.Audit.Record(audit.Entry{Actor: "system:ttl", Role: "system", Project: project, Action: "machine.expire", MachineID: m.ID, Result: audit.ResultSuccess, Detail: "TTL expired at " + expires.UTC().Format(time.RFC3339)})
			}
			if r.OnExpire != nil {
				r.OnExpire(project)
			}
			r.Events.Publish(ctx, "io.kryton.machine.expired", "machines/"+m.ID, map[string]any{"project": project, "machineId": m.ID, "name": m.Spec.Name})
		}
	}
}

// expire deletes one expired machine unless it is protected; it reports
// whether the machine was deleted. A blocked expiry is announced once.
func (r TTL) expire(ctx context.Context, project string, m model.Machine) bool {
	if r.Guard != nil {
		release, err := r.Guard.BeginDelete(project, m.ID)
		if err != nil {
			if r.held == nil || !r.held[m.ID] {
				if r.held != nil {
					r.held[m.ID] = true
				}
				if r.Audit != nil {
					_, _ = r.Audit.Record(audit.Entry{Actor: "system:ttl", Role: "system", Project: project, Action: "machine.expire", MachineID: m.ID, Result: audit.ResultDenied, Detail: "TTL expired but " + err.Error()})
				}
				r.Events.Publish(ctx, "io.kryton.machine.expiry.blocked", "machines/"+m.ID, map[string]any{"project": project, "machineId": m.ID, "name": m.Spec.Name, "reason": err.Error()})
			}
			return false
		}
		defer release()
	}
	if err := r.Provider.Delete(ctx, project, m.ID); err != nil {
		r.Log.Warn("ttl delete failed", "project", project, "machine", m.ID, "error", err)
		return false
	}
	return true
}
