// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

var labelKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:/-]{1,128}$`)

// ---- Machine list filters --------------------------------------------------

var knownStates = map[model.MachineState]bool{
	model.StatePending: true, model.StateProvisioning: true, model.StateStarting: true,
	model.StateRunning: true, model.StateStopping: true, model.StateStopped: true,
	model.StateMigrating: true, model.StatePaused: true, model.StateDeleting: true,
	model.StateFailed: true, model.StateUnknown: true,
}

// machineFilter is the state/name part of GET /machines, applied alongside
// the label selector and before pagination.
type machineFilter struct {
	states map[model.MachineState]bool
	name   string // lower-cased substring
}

func parseMachineFilter(r *http.Request) (machineFilter, error) {
	var f machineFilter
	q := r.URL.Query()
	if v := q.Get("state"); v != "" {
		parts := strings.Split(v, ",")
		if len(parts) > len(knownStates) {
			return f, errors.New("too many state values")
		}
		f.states = map[model.MachineState]bool{}
		for _, p := range parts {
			st := model.MachineState(strings.TrimSpace(p))
			if !knownStates[st] {
				return f, errors.New("state must be a comma-separated list of machine states (pending, provisioning, starting, running, stopping, stopped, migrating, paused, deleting, failed, unknown)")
			}
			f.states[st] = true
		}
	}
	if v := q.Get("name"); v != "" {
		if len(v) > 128 {
			return f, errors.New("name filter is at most 128 characters")
		}
		f.name = strings.ToLower(v)
	}
	return f, nil
}

func (f machineFilter) apply(ms []model.Machine) []model.Machine {
	if f.states == nil && f.name == "" {
		return ms
	}
	out := ms[:0:0]
	for _, m := range ms {
		if f.states != nil && !f.states[m.State] {
			continue
		}
		if f.name != "" && !strings.Contains(strings.ToLower(m.Spec.Name), f.name) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// ---- allowedActions (Redfish-style discoverability) --------------------------

// actionContext captures what the backend supports so a list of machines
// needs one Capabilities call, not one per machine.
type actionContext struct {
	snapshots bool
	restart   bool
}

func (s *Server) actionContext(ctx context.Context) actionContext {
	var ac actionContext
	if c, err := s.p.Capabilities(ctx); err == nil {
		ac.snapshots = c.Snapshots
	}
	_, ac.restart = s.p.(provider.GuestProvider)
	return ac
}

// actionsFor derives the actions a machine accepts in its current state.
// Advisory: the provider still has the last word.
func (ac actionContext) actionsFor(st model.MachineState) []string {
	var out []string
	switch st {
	case model.StateRunning:
		out = append(out, "stop")
		if ac.restart {
			out = append(out, "restart")
		}
		if ac.snapshots {
			out = append(out, "snapshot")
		}
		out = append(out, "delete")
	case model.StateStopped:
		out = append(out, "start")
		if ac.snapshots {
			out = append(out, "snapshot")
		}
		out = append(out, "delete")
	case model.StateFailed:
		out = append(out, "start", "delete")
	case model.StatePending, model.StateProvisioning, model.StateUnknown, model.StatePaused:
		out = append(out, "delete")
	}
	return out
}

func (ac actionContext) annotate(m *model.Machine) { m.AllowedActions = ac.actionsFor(m.State) }

func hasAction(list []string, a string) bool {
	for _, x := range list {
		if x == a {
			return true
		}
	}
	return false
}
