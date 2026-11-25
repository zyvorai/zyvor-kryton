// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package schedule

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
	"unicode/utf8"

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

	mu      sync.Mutex
	running map[string]bool
}

// ErrBusy is returned by RunNow while the schedule is already executing.
var ErrBusy = errors.New("schedule is already running")

// claim marks a schedule as executing so a manual run and the ticker never
// run it at the same time.
func (r *Runner) claim(sid string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running[sid] {
		return false
	}
	if r.running == nil {
		r.running = map[string]bool{}
	}
	r.running[sid] = true
	return true
}

func (r *Runner) unclaim(sid string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.running, sid)
}

func (r *Runner) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now().UTC()
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
	now := r.now()
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
		r.runDue(ctx, sc, now)
	}
}

func (r *Runner) runDue(ctx context.Context, due Schedule, now time.Time) {
	if !r.claim(due.ID) {
		return // a manual run is in progress; the slot is picked up next poll
	}
	defer r.unclaim(due.ID)
	// Re-read: it may have been edited, disabled or deleted since Due.
	sc, err := r.Store.Get(due.ID)
	if err != nil || !sc.Enabled || sc.NextRunAt == nil || sc.NextRunAt.After(now) {
		return
	}
	warning, err := r.execute(ctx, sc)
	switch {
	case err == nil:
		r.Store.MarkRun(sc.ID, now, "success", warning)
		r.record(sc, "success", warning)
		r.outcome(sc.Action, "success")
	case errors.Is(err, provider.ErrNotFound):
		// The machine is gone: its schedule can never succeed again.
		_ = r.Store.Delete(sc.ID)
		r.record(sc, "error", "machine no longer exists; schedule removed")
		r.outcome(sc.Action, "machine-gone")
	default:
		disabled := r.Store.MarkRun(sc.ID, now, "error", err.Error())
		r.record(sc, "error", err.Error())
		r.outcome(sc.Action, "error")
		if disabled {
			r.announceDisabled(sc, err)
		}
		if r.Log != nil {
			r.Log.Warn("scheduled action failed", "schedule", sc.ID, "project", sc.Project, "machine", sc.MachineID, "action", sc.Action, "error", err)
		}
	}
}

// RunNow executes a schedule immediately, whether or not it is enabled. The
// next scheduled slot is unchanged. The returned error is the action's.
func (r *Runner) RunNow(ctx context.Context, sid string) (Schedule, error) {
	if !r.claim(sid) {
		return Schedule{}, ErrBusy
	}
	defer r.unclaim(sid)
	sc, err := r.Store.Get(sid)
	if err != nil {
		return Schedule{}, err
	}
	warning, runErr := r.execute(ctx, sc)
	result, detail := "success", warning
	if runErr != nil {
		result, detail = "error", runErr.Error()
	}
	out, markErr := r.Store.MarkManualRun(sid, r.now(), result, detail)
	note := "manual run"
	if detail != "" {
		note += ": " + detail
	}
	r.record(sc, result, note)
	r.outcome(sc.Action, result)
	if runErr != nil {
		return out, runErr
	}
	return out, markErr
}

func (r *Runner) announceDisabled(sc Schedule, cause error) {
	if r.Events != nil {
		r.Events.Publish(context.Background(), "io.kryton.schedule.disabled", "schedules/"+sc.ID,
			map[string]any{"project": sc.Project, "scheduleId": sc.ID, "machineId": sc.MachineID, "action": sc.Action, "maxFailures": sc.MaxFailures, "lastError": cause.Error()})
	}
}

func (r *Runner) outcome(action, outcome string) {
	if r.OnRun != nil {
		r.OnRun(action, outcome)
	}
}

// execute performs the schedule's action. warning carries a problem that did
// not fail the action (retention could not delete an old snapshot).
func (r *Runner) execute(ctx context.Context, sc Schedule) (warning string, err error) {
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
			return "", err
		}
		pub("io.kryton.machine.started", map[string]any{"name": m.Spec.Name, "state": m.State, "trigger": "schedule"})
	case ActionStop:
		m, err := r.Provider.Stop(ctx, sc.Project, sc.MachineID)
		if err != nil {
			return "", err
		}
		pub("io.kryton.machine.stopped", map[string]any{"name": m.Spec.Name, "state": m.State, "trigger": "schedule"})
	case ActionRestart:
		if _, err := r.Provider.Stop(ctx, sc.Project, sc.MachineID); err != nil {
			return "", err
		}
		m, err := r.Provider.Start(ctx, sc.Project, sc.MachineID)
		if err != nil {
			return "", fmt.Errorf("restart: stopped but start failed: %w", err)
		}
		pub("io.kryton.machine.restarted", map[string]any{"name": m.Spec.Name, "state": m.State, "trigger": "schedule"})
	case ActionSnapshot:
		name := snapshotName(sc, r.now())
		snap, err := r.Provider.Snapshot(ctx, sc.Project, sc.MachineID, name)
		if err != nil {
			return "", err
		}
		pub("io.kryton.snapshot.created", map[string]any{"snapshotId": snap.ID, "name": snap.Name, "trigger": "schedule"})
		if sc.Retain > 0 {
			if err := r.Store.TrackSnapshot(sc.ID, snap.ID); err != nil {
				return "retention: could not record the snapshot: " + err.Error(), nil
			}
			if err := r.prune(ctx, sc, snap.ID); err != nil {
				return "retention: " + err.Error(), nil
			}
		}
	default:
		return "", fmt.Errorf("unknown action %q", sc.Action)
	}
	return "", nil
}

// snapshotName is the name of the next scheduled snapshot. With retention the
// schedule keeps several, so a fixed name gets a timestamp suffix (trimmed to
// keep the whole name within 63 characters).
func snapshotName(sc Schedule, now time.Time) string {
	stamp := now.UTC().Format("20060102-150405")
	switch {
	case sc.SnapshotName == "":
		return "scheduled-" + stamp
	case sc.Retain > 0:
		base := sc.SnapshotName
		for len(base) > 63-len(stamp)-1 {
			_, size := utf8.DecodeLastRuneInString(base)
			base = base[:len(base)-size]
		}
		return base + "-" + stamp
	}
	return sc.SnapshotName
}

// prune deletes the schedule's snapshots older than the Retain-th newest
// usable one. Until that many are usable nothing is deleted, so a run of
// failed or still-copying snapshots never removes the last good ones.
func (r *Runner) prune(ctx context.Context, sc Schedule, justTaken string) error {
	cur, err := r.Store.Get(sc.ID)
	if err != nil || len(cur.SnapshotIDs) <= sc.Retain {
		return err
	}
	snaps, err := r.Provider.ListSnapshots(ctx, sc.Project, sc.MachineID)
	if err != nil {
		return fmt.Errorf("list snapshots: %w", err)
	}
	state := make(map[string]string, len(snaps))
	for _, s := range snaps {
		state[s.ID] = s.State
	}
	var live, gone []string
	for _, sid := range cur.SnapshotIDs {
		if _, ok := state[sid]; ok || sid == justTaken {
			live = append(live, sid)
		} else {
			gone = append(gone, sid)
		}
	}
	usable, cut := 0, 0
	for i := len(live) - 1; i >= 0; i-- {
		if st := state[live[i]]; st == "ready" || st == "restored" {
			if usable++; usable == sc.Retain {
				cut = i
				break
			}
		}
	}
	var failures []error
	removed := 0
	for _, sid := range live[:cut] {
		err := r.Provider.DeleteSnapshot(ctx, sc.Project, sc.MachineID, sid)
		if err != nil && !errors.Is(err, provider.ErrNotFound) {
			failures = append(failures, fmt.Errorf("delete snapshot %s: %w", sid, err))
			continue // stays tracked, retried next run
		}
		gone = append(gone, sid)
		removed++
		if r.Events != nil {
			r.Events.Publish(ctx, "io.kryton.snapshot.deleted", "snapshots/"+sid,
				map[string]any{"project": sc.Project, "machineId": sc.MachineID, "snapshotId": sid, "scheduleId": sc.ID, "trigger": "schedule-retention"})
		}
	}
	if removed > 0 && r.Audit != nil {
		_, _ = r.Audit.Record(audit.Entry{Actor: "system:scheduler", Role: "system", Project: sc.Project, Action: "snapshot.retention",
			MachineID: sc.MachineID, Result: audit.ResultSuccess, Detail: fmt.Sprintf("schedule %s removed %d old snapshot(s), keeping %d", sc.ID, removed, sc.Retain)})
	}
	if err := r.Store.UntrackSnapshots(sc.ID, gone...); err != nil {
		failures = append(failures, err)
	}
	return errors.Join(failures...)
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
