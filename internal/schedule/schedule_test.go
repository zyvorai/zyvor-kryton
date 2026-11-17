// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package schedule

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zyvorai/kryton/internal/audit"
	"github.com/zyvorai/kryton/internal/demo"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/model"
)

func mustCron(t *testing.T, s string) *Cron {
	t.Helper()
	c, err := ParseCron(s)
	if err != nil {
		t.Fatalf("%q: %v", s, err)
	}
	return c
}

func TestCronNext(t *testing.T) {
	utc := time.UTC
	base := time.Date(2026, 3, 4, 10, 17, 30, 0, utc) // a Wednesday
	cases := []struct {
		expr string
		want time.Time
	}{
		{"*/15 * * * *", time.Date(2026, 3, 4, 10, 30, 0, 0, utc)},
		{"0 18 * * mon-fri", time.Date(2026, 3, 4, 18, 0, 0, 0, utc)},
		{"0 18 * * 1-5", time.Date(2026, 3, 4, 18, 0, 0, 0, utc)},
		{"0 8 * * 1", time.Date(2026, 3, 9, 8, 0, 0, 0, utc)},
		{"0 8 * * 7", time.Date(2026, 3, 8, 8, 0, 0, 0, utc)},
		{"30 2 1 * *", time.Date(2026, 4, 1, 2, 30, 0, 0, utc)},
		{"0 0 29 feb *", time.Date(2028, 2, 29, 0, 0, 0, 0, utc)},
		{"@hourly", time.Date(2026, 3, 4, 11, 0, 0, 0, utc)},
		{"@daily", time.Date(2026, 3, 5, 0, 0, 0, 0, utc)},
		{"5,35 10,12 * * *", time.Date(2026, 3, 4, 10, 35, 0, 0, utc)},
		{"10-20/5 * * * *", time.Date(2026, 3, 4, 10, 20, 0, 0, utc)},
		// dom and dow both restricted: either may match (Vixie semantics).
		{"0 9 5 * thu", time.Date(2026, 3, 5, 9, 0, 0, 0, utc)},
	}
	for _, c := range cases {
		got := mustCron(t, c.expr).Next(base)
		if !got.Equal(c.want) {
			t.Errorf("%q: got %v want %v", c.expr, got, c.want)
		}
	}
}

func TestCronInvalid(t *testing.T) {
	for _, e := range []string{"", "* * * *", "61 * * * *", "* 24 * * *", "* * 0 * *", "* * * 13 *", "*/0 * * * *", "a b c d e", "5-1 * * * *", "* * * * * *", "1,,2 * * * *"} {
		if _, err := ParseCron(e); err == nil {
			t.Errorf("%q should be invalid", e)
		}
	}
	if _, err := LoadLocation("Mars/Olympus"); err == nil {
		t.Error("bad timezone accepted")
	}
}

func TestCronTimezoneAndDST(t *testing.T) {
	ny, err := LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	// 09:00 New York is 14:00 UTC in winter and 13:00 UTC in summer.
	c := mustCron(t, "0 9 * * *")
	w := c.Next(time.Date(2026, 1, 15, 0, 0, 0, 0, ny))
	if w.UTC().Hour() != 14 {
		t.Fatalf("winter: %v", w.UTC())
	}
	s := c.Next(time.Date(2026, 7, 15, 0, 0, 0, 0, ny))
	if s.UTC().Hour() != 13 {
		t.Fatalf("summer: %v", s.UTC())
	}
	// Spring forward (2026-03-08): a daily 02:30 job skips the missing hour that day.
	got := mustCron(t, "30 2 * * *").Next(time.Date(2026, 3, 8, 0, 0, 0, 0, ny))
	if got.Day() == 8 && got.Hour() == 2 {
		t.Fatalf("matched a non-existent time: %v", got)
	}
	// Fall back (2026-11-01): 01:30 happens twice; fire once.
	c2 := mustCron(t, "30 1 * * *")
	first := c2.Next(time.Date(2026, 11, 1, 0, 0, 0, 0, ny))
	second := c2.Next(first)
	if second.Sub(first) < 20*time.Hour {
		t.Fatalf("fired twice in the repeated hour: %v then %v", first, second)
	}
}

func TestStoreCRUDAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "s.json")
	now := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	st, err := NewStore(path, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Schedule{
		{Project: "p", MachineID: "m", Action: "reboot", Cron: "* * * * *"},
		{Project: "p", MachineID: "m", Action: "stop", Cron: "nope"},
		{Project: "p", MachineID: "m", Action: "stop", Cron: "0 0 * * *", Timezone: "Nowhere/Land"},
		{Project: "p", Action: "stop", Cron: "0 0 * * *"},
	} {
		if _, err := st.Create(bad); err == nil {
			t.Errorf("accepted invalid %+v", bad)
		}
	}
	sc, err := st.Create(Schedule{Project: "p", MachineID: "m1", Action: ActionStop, Cron: "0 19 * * mon-fri", Timezone: "Europe/Berlin", CreatedBy: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	// Wed 2026-03-04 19:00 CET = 18:00 UTC.
	if sc.NextRunAt == nil || !sc.NextRunAt.Equal(time.Date(2026, 3, 4, 18, 0, 0, 0, time.UTC)) {
		t.Fatalf("next run: %v", sc.NextRunAt)
	}
	disabled := false
	cron := "0 6 * * *"
	up, err := st.Update(sc.ID, Patch{Enabled: &disabled, Cron: &cron})
	if err != nil || up.Enabled || up.Cron != cron {
		t.Fatalf("update: %+v %v", up, err)
	}
	if _, err := st.Update(sc.ID, Patch{Cron: strPtr("bad")}); err == nil {
		t.Fatal("update accepted bad cron")
	}
	exp := now.Add(time.Hour)
	if _, err := st.SetExpiry("p", "m1", &exp, "alice"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.SetExpiry("p", "m2", nil, "alice"); err != nil {
		t.Fatal(err)
	}

	st2, err := NewStore(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if l := st2.List("p", "", nil); len(l) != 1 || l[0].Enabled {
		t.Fatalf("reload: %+v", l)
	}
	if at, ok := st2.Expiry("p", "m1"); !ok || at == nil || !at.Equal(exp) {
		t.Fatalf("expiry reload: %v %v", at, ok)
	}
	if at, ok := st2.Expiry("p", "m2"); !ok || at != nil {
		t.Fatal("never-expire override lost")
	}
	if n := st2.ForgetMachine("p", "m1"); n != 1 {
		t.Fatalf("forget removed %d", n)
	}
	if _, ok := st2.Expiry("p", "m1"); ok {
		t.Fatal("expiry survived ForgetMachine")
	}
	if err := st2.Delete("nope"); err != ErrNotFound {
		t.Fatal("delete unknown")
	}
}

func strPtr(s string) *string { return &s }

func TestStoreLimit(t *testing.T) {
	st, _ := NewStore("", nil)
	for i := 0; i < MaxPerProject; i++ {
		if _, err := st.Create(Schedule{Project: "p", MachineID: "m", Action: ActionStart, Cron: "0 0 * * *"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Create(Schedule{Project: "p", MachineID: "m", Action: ActionStart, Cron: "0 0 * * *"}); err != ErrLimit {
		t.Fatalf("limit: %v", err)
	}
}

func TestRunnerExecutesActions(t *testing.T) {
	ctx := context.Background()
	p := demo.New()
	m, err := p.Create(ctx, "lab", model.MachineSpec{Name: "vm", Image: "x", Compute: model.ComputeSpec{CPU: 2, MemoryMiB: 2048}, Disk: model.DiskSpec{SizeGiB: 20}})
	if err != nil {
		t.Fatal(err)
	}
	clock := time.Date(2026, 3, 4, 17, 59, 0, 0, time.UTC)
	st, _ := NewStore("", func() time.Time { return clock })
	bus, _ := events.New(50, "", "", "", nil)
	au, _ := audit.New(audit.Config{})
	var outcomes []string
	r := &Runner{Store: st, Provider: p, Events: bus, Audit: au, Now: func() time.Time { return clock },
		OnRun: func(a, o string) { outcomes = append(outcomes, a+":"+o) }}

	stop, _ := st.Create(Schedule{Project: "lab", MachineID: m.ID, Action: ActionStop, Cron: "0 18 * * *"})
	snap, _ := st.Create(Schedule{Project: "lab", MachineID: m.ID, Action: ActionSnapshot, Cron: "0 18 * * *", SnapshotName: "nightly"})
	missing, _ := st.Create(Schedule{Project: "lab", MachineID: "ghost", Action: ActionStart, Cron: "0 18 * * *"})

	r.RunOnce(ctx) // 17:59 - nothing due
	if len(outcomes) != 0 {
		t.Fatalf("ran early: %v", outcomes)
	}
	clock = time.Date(2026, 3, 4, 18, 0, 30, 0, time.UTC)
	r.RunOnce(ctx)
	got, _ := p.Get(ctx, "lab", m.ID)
	if got.State != model.StateStopped {
		t.Fatalf("machine not stopped: %s", got.State)
	}
	snaps, _ := p.ListSnapshots(ctx, "lab", m.ID)
	if len(snaps) != 1 || snaps[0].Name != "nightly" {
		t.Fatalf("snapshots: %+v", snaps)
	}
	if _, err := st.Get(missing.ID); err != ErrNotFound {
		t.Fatal("schedule for a deleted machine should be removed")
	}
	s1, _ := st.Get(stop.ID)
	if s1.LastResult != "success" || s1.RunCount != 1 || s1.NextRunAt == nil || !s1.NextRunAt.After(clock) {
		t.Fatalf("stop schedule state: %+v", s1)
	}
	_ = snap
	ents, _, _ := au.Query(audit.Filter{Action: "schedule.run"})
	if len(ents) != 3 || ents[0].Actor != "system:scheduler" {
		t.Fatalf("audit entries: %+v", ents)
	}
	evs := bus.List(0)
	var sawStop, sawExec bool
	for _, e := range evs {
		sawStop = sawStop || e.Type == "io.kryton.machine.stopped"
		sawExec = sawExec || e.Type == "io.kryton.schedule.executed"
	}
	if !sawStop || !sawExec {
		t.Fatalf("events missing: %+v", evs)
	}

	// restart: stop+start leaves the machine running.
	rs, _ := st.Create(Schedule{Project: "lab", MachineID: m.ID, Action: ActionRestart, Cron: "5 18 * * *"})
	clock = time.Date(2026, 3, 4, 18, 5, 10, 0, time.UTC)
	r.RunOnce(ctx)
	got, _ = p.Get(ctx, "lab", m.ID)
	if got.State != model.StateRunning {
		t.Fatalf("restart should end running, got %s", got.State)
	}
	if x, _ := st.Get(rs.ID); x.LastResult != "success" {
		t.Fatalf("restart result: %+v", x)
	}
}

func TestRunnerSkipsMissedSlots(t *testing.T) {
	ctx := context.Background()
	p := demo.New()
	m, _ := p.Create(ctx, "lab", model.MachineSpec{Name: "vm", Image: "x", Compute: model.ComputeSpec{CPU: 1, MemoryMiB: 1024}, Disk: model.DiskSpec{SizeGiB: 16}})
	clock := time.Date(2026, 3, 4, 17, 0, 0, 0, time.UTC)
	st, _ := NewStore("", func() time.Time { return clock })
	sc, _ := st.Create(Schedule{Project: "lab", MachineID: m.ID, Action: ActionStop, Cron: "0 18 * * *"})
	clock = time.Date(2026, 3, 4, 22, 0, 0, 0, time.UTC) // daemon was down for hours
	r := &Runner{Store: st, Provider: p, Now: func() time.Time { return clock }}
	r.RunOnce(ctx)
	got, _ := p.Get(ctx, "lab", m.ID)
	if got.State != model.StateRunning {
		t.Fatal("stale run must not execute")
	}
	x, _ := st.Get(sc.ID)
	if x.LastResult != "skipped" || x.NextRunAt == nil || !x.NextRunAt.After(clock) {
		t.Fatalf("expected skipped + rescheduled: %+v", x)
	}
}
