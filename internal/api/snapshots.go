// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"errors"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

// maxSnapshotScan bounds how many machines one inventory request reads.
const maxSnapshotScan = 500

type inventorySnapshot struct {
	model.Snapshot
	MachineName string `json:"machineName"`
	// ScheduleID names the schedule whose retention manages this snapshot.
	ScheduleID string `json:"scheduleId,omitempty"`
}

type snapshotInventoryError struct {
	MachineID string   `json:"machineId"`
	Error     APIError `json:"error"`
}

type snapshotInventory struct {
	Project string              `json:"project"`
	Items   []inventorySnapshot `json:"items"`
	// Total counts every match, including those beyond limit.
	Total    int `json:"total"`
	Machines int `json:"machines"`
	// Errors lists machines whose snapshots could not be read; the result is
	// partial when it is not empty.
	Errors []snapshotInventoryError `json:"errors,omitempty"`
}

// parseAge reads a positive Go duration ("36h") or a number of days ("30d").
func parseAge(v string) (time.Duration, error) {
	bad := errors.New(`olderThan must be a positive duration such as "36h" or "30d" (at most 3650d)`)
	const max = 3650 * 24 * time.Hour
	if days, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(days)
		if err != nil || n <= 0 || n > 3650 {
			return 0, bad
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 || d > max {
		return 0, bad
	}
	return d, nil
}

// listProjectSnapshots lists snapshots across a project's machines, oldest
// first, so stale ones are easy to find. It is read-only.
func (s *Server) listProjectSnapshots(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	q := r.URL.Query()
	sel, err := parseSelectorParam(q.Get("selector"))
	if err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	var cutoff time.Time
	if v := q.Get("olderThan"); v != "" {
		age, err := parseAge(v)
		if err != nil {
			s.badRequest(w, r, err.Error())
			return
		}
		cutoff = time.Now().UTC().Add(-age)
	}
	limit := 100
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 500 {
			s.badRequest(w, r, "limit must be between 1 and 500")
			return
		}
		limit = n
	}
	caps, err := s.p.Capabilities(r.Context())
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	if !caps.Snapshots {
		s.writeErr(w, r, provider.ErrUnsupported)
		return
	}
	ms, err := s.p.List(r.Context(), project)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	ms = filterBySelector(ms, sel)
	if len(ms) > maxSnapshotScan {
		s.badRequest(w, r, "more than 500 machines match; narrow them with a selector")
		return
	}
	sort.Slice(ms, func(i, j int) bool { return ms[i].ID < ms[j].ID })
	managed := map[string]string{}
	if s.schedules != nil {
		for _, sc := range s.schedules.List(project, "", nil) {
			if sc.Retain > 0 {
				for _, id := range sc.SnapshotIDs {
					managed[id] = sc.ID
				}
			}
		}
	}
	out := snapshotInventory{Project: project, Items: []inventorySnapshot{}, Machines: len(ms)}
	for _, m := range ms {
		if err := r.Context().Err(); err != nil {
			s.writeErr(w, r, err)
			return
		}
		snaps, err := s.p.ListSnapshots(r.Context(), project, m.ID)
		if errors.Is(err, provider.ErrNotFound) {
			continue // deleted while we were reading
		}
		if err != nil {
			c := classifyError(err)
			out.Errors = append(out.Errors, snapshotInventoryError{MachineID: m.ID, Error: APIError{Code: c.Code, Message: c.Message}})
			continue
		}
		for _, sn := range snaps {
			if !cutoff.IsZero() && sn.CreatedAt.After(cutoff) {
				continue
			}
			out.Items = append(out.Items, inventorySnapshot{Snapshot: sn, MachineName: m.Spec.Name, ScheduleID: managed[sn.ID]})
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if !a.CreatedAt.Equal(b.CreatedAt) {
			return a.CreatedAt.Before(b.CreatedAt)
		}
		return a.ID < b.ID
	})
	out.Total = len(out.Items)
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
	}
	jsonResponse(w, http.StatusOK, out)
}
