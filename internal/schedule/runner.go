// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/zyvorai/kryton/internal/audit"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/provider"
)

// Runner executes due schedules against a provider.
type Runner struct {
	Store    *Store
	Provider provider.Provider
	Events   *events.Bus
	Audit    *audit.Store
	Log      *slog.Logger
	Interval time.Duration // poll period, default 15s
	// CatchUp is how late a run may start after its slot (e.g. after a
	// restart) before it is skipped instead; default 5 minutes.
	CatchUp time.Duration
	Now     func() time.Time
	// OnRun is called after each execution (metrics hook): action, outcome.
	OnRun func(action, outcome string)
}

// Run polls until ctx is canceled.
func (r *Runner) Run(ctx context.Context) {
	if r.Interval <= 0 {
		r.Interval = 15 * time.Second
	}
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	r.RunOnce(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.RunOnce(ctx)
		}
	}
}

// RunOnce executes every schedule that is due now.
func (r *Runner) RunOnce(ctx context.Context) {
	now := time.Now().UTC()
	if r.Now != nil {
		now = r.Now()
	}
	catchUp := r.CatchUp
	if catchUp <= 0 {
		catchUp = 5 * time.Minute
	}
	for _, sc := range r.Store.Due(now) {
		if now.Sub(*sc.NextRunAt) > catchUp {
			r.Store.MarkRun(sc.ID, now, "skipped", "missed its slot by more than the catch-up window")
			r.record(sc, "skipped", "missed its slot")
			r.outcome(sc.Action, "skipped")
			continue
		}
		err := r.execute(ctx, sc)
		switch {
		case err == nil:
			r.Store.MarkRun(sc.ID, now, "success", "")
			r.record(sc, "success", "")
			r.outcome(sc.Action, "success")
		case errors.Is(err, provider.ErrNotFound):
			// The machine is gone: its schedule can never succeed again.
			_ = r.Store.Delete(sc.ID)
			r.record(sc, "error", "machine no longer exists; schedule removed")
			r.outcome(sc.Action, "machine-gone")
		default:
			r.Store.MarkRun(sc.ID, now, "error", err.Error())
			r.record(sc, "error", err.Error())
			r.outcome(sc.Action, "error")
			if r.Log != nil {
				r.Log.Warn("scheduled action failed", "schedule", sc.ID, "project", sc.Project, "machine", sc.MachineID, "action", sc.Action, "error", err)
			}
		}
	}
}

func (r *Runner) outcome(action, outcome string) {
	if r.OnRun != nil {
		r.OnRun(action, outcome)
	}
}

func (r *Runner) execute(ctx context.Context, sc Schedule) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	pub := func(typ string, data map[string]any) {
		if r.Events != nil {
			data["project"], data["machineId"], data["scheduleId"] = sc.Project, sc.MachineID, sc.ID
			r.Events.Publish(ctx, typ, "machines/"+sc.MachineID, data)
		}
	}
	switch sc.Action {
	case ActionStart:
		m, err := r.Provider.Start(ctx, sc.Project, sc.MachineID)
		if err != nil {
			return err
		}
		pub("io.kryton.machine.started", map[string]any{"name": m.Spec.Name, "state": m.State, "trigger": "schedule"})
	case ActionStop:
		m, err := r.Provider.Stop(ctx, sc.Project, sc.MachineID)
		if err != nil {
			return err
		}
		pub("io.kryton.machine.stopped", map[string]any{"name": m.Spec.Name, "state": m.State, "trigger": "schedule"})
	case ActionRestart:
		if _, err := r.Provider.Stop(ctx, sc.Project, sc.MachineID); err != nil {
			return err
		}
		m, err := r.Provider.Start(ctx, sc.Project, sc.MachineID)
		if err != nil {
			return fmt.Errorf("restart: stopped but start failed: %w", err)
		}
		pub("io.kryton.machine.restarted", map[string]any{"name": m.Spec.Name, "state": m.State, "trigger": "schedule"})
	case ActionSnapshot:
		name := sc.SnapshotName
		if name == "" {
			name = "scheduled-" + time.Now().UTC().Format("20060102-150405")
		}
		snap, err := r.Provider.Snapshot(ctx, sc.Project, sc.MachineID, name)
		if err != nil {
			return err
		}
		pub("io.kryton.snapshot.created", map[string]any{"snapshotId": snap.ID, "name": snap.Name, "trigger": "schedule"})
	default:
		return fmt.Errorf("unknown action %q", sc.Action)
	}
	return nil
}

func (r *Runner) record(sc Schedule, result, detail string) {
	if r.Audit != nil {
		d := fmt.Sprintf("schedule %s: %s", sc.ID, sc.Action)
		if detail != "" {
			d += " (" + detail + ")"
		}
		_, _ = r.Audit.Record(audit.Entry{Actor: "system:scheduler", Role: "system", Project: sc.Project, Action: "schedule.run",
			MachineID: sc.MachineID, Result: result, Detail: d})
	}
	if r.Events != nil {
		r.Events.Publish(context.Background(), "io.kryton.schedule.executed", "schedules/"+sc.ID,
			map[string]any{"project": sc.Project, "scheduleId": sc.ID, "machineId": sc.MachineID, "action": sc.Action, "result": result, "detail": detail})
	}
}
