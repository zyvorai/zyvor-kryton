// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package schedule

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/zyvorai/kryton/internal/demo"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

type gatedProvider struct {
	provider.Provider
	fail    bool
	entered chan struct{}
	release chan struct{}
}

func (g *gatedProvider) Snapshot(ctx context.Context, p, m, name string) (*model.Snapshot, error) {
	if g.fail {
		return nil, errors.New("backend down")
	}
	if g.entered != nil {
		g.entered <- struct{}{}
		<-g.release
	}
	return g.Provider.Snapshot(ctx, p, m, name)
}

func gatedFixture(t *testing.T, now func() time.Time) (*Store, *gatedProvider, string) {
	t.Helper()
	st, err := NewStore(filepath.Join(t.TempDir(), "s.json"), now)
	if err != nil {
		t.Fatal(err)
	}
	p := demo.New()
	m, err := p.Create(context.Background(), "lab", model.MachineSpec{Name: "vm", Image: "windows-server-2025"})
	if err != nil {
		t.Fatal(err)
	}
	return st, &gatedProvider{Provider: p}, m.ID
}

func TestMaxFailuresDisablesAndResumeResets(t *testing.T) {
	clock := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	st, gp, mid := gatedFixture(t, now)
	bus, err := events.New(20, "", "", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &Runner{Store: st, Provider: gp, Events: bus, Now: now}
	sc, err := st.Create(Schedule{Project: "lab", MachineID: mid, Action: ActionSnapshot, Cron: "* * * * *", MaxFailures: 3})
	if err != nil {
		t.Fatal(err)
	}
	step := func() Schedule {
		clock = clock.Add(time.Minute)
		r.RunOnce(context.Background())
		got, err := st.Get(sc.ID)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	gp.fail = true
	step()
	step()
	gp.fail = false
	if got := step(); got.ConsecutiveFailures != 0 || !got.Enabled {
		t.Fatalf("success must reset the count: %+v", got)
	}
	gp.fail = true
	step()
	if got := step(); !got.Enabled || got.ConsecutiveFailures != 2 {
		t.Fatalf("disabled too early: %+v", got)
	}
	got := step()
	if got.Enabled || got.ConsecutiveFailures != 3 || !strings.Contains(got.DisabledReason, "3 consecutive") {
		t.Fatalf("not disabled: %+v", got)
	}
	if after := step(); after.RunCount != got.RunCount {
		t.Fatal("a disabled schedule ran")
	}
	var announced int
	for _, e := range bus.List(50) {
		if e.Type == "io.kryton.schedule.disabled" {
			announced++
		}
	}
	if announced != 1 {
		t.Fatalf("disabled events = %d", announced)
	}
	// The state survives a restart, and enabling clears it.
	again, err := NewStore(st.path, now)
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := again.Get(sc.ID); got.Enabled || got.DisabledReason == "" {
		t.Fatalf("reloaded = %+v", got)
	}
	on := true
	resumed, err := again.Update(sc.ID, Patch{Enabled: &on})
	if err != nil || !resumed.Enabled || resumed.ConsecutiveFailures != 0 || resumed.DisabledReason != "" {
		t.Fatalf("resumed = %+v %v", resumed, err)
	}
}

func TestZeroMaxFailuresNeverDisables(t *testing.T) {
	st, _, mid := gatedFixture(t, nil)
	sc, err := st.Create(Schedule{Project: "lab", MachineID: mid, Action: ActionStart, Cron: "@daily"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		if st.MarkRun(sc.ID, time.Now(), "error", "x") {
			t.Fatal("disabled")
		}
	}
}

func TestRunNowAndTickerNeverOverlap(t *testing.T) {
	clock := time.Date(2026, 3, 4, 10, 0, 0, 0, time.UTC)
	now := func() time.Time { return clock }
	st, gp, mid := gatedFixture(t, now)
	gp.entered, gp.release = make(chan struct{}, 4), make(chan struct{})
	r := &Runner{Store: st, Provider: gp, Now: now}
	sc, err := st.Create(Schedule{Project: "lab", MachineID: mid, Action: ActionSnapshot, Cron: "* * * * *", Retain: 1})
	if err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(time.Minute)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		if _, err := r.RunNow(context.Background(), sc.ID); err != nil {
			t.Errorf("RunNow: %v", err)
		}
	}()
	<-gp.entered
	if _, err := r.RunNow(context.Background(), sc.ID); !errors.Is(err, ErrBusy) {
		t.Fatalf("second RunNow = %v", err)
	}
	r.RunOnce(context.Background()) // must skip, not block
	if got, _ := st.Get(sc.ID); got.RunCount != 0 {
		t.Fatalf("ticker ran concurrently: %+v", got)
	}
	close(gp.release)
	wg.Wait()
	r.RunOnce(context.Background()) // the slot is still due
	if got, _ := st.Get(sc.ID); got.RunCount != 2 || len(got.SnapshotIDs) != 1 {
		t.Fatalf("after = %+v", got)
	}
}

func TestSnapshotNameSuffixFitsLimit(t *testing.T) {
	now := time.Date(2026, 3, 4, 10, 11, 12, 0, time.UTC)
	for _, base := range []string{"nightly", strings.Repeat("a", 63), strings.Repeat("é", 31)} {
		got := snapshotName(Schedule{SnapshotName: base, Retain: 3}, now)
		if len(got) > 63 || !utf8.ValidString(got) || !strings.HasSuffix(got, "-20260304-101112") {
			t.Errorf("%q -> %q (%d)", base, got, len(got))
		}
	}
	if got := snapshotName(Schedule{SnapshotName: "fixed"}, now); got != "fixed" {
		t.Errorf("without retention the name is unchanged, got %q", got)
	}
	if got := snapshotName(Schedule{}, now); got != "scheduled-20260304-101112" {
		t.Errorf("default = %q", got)
	}
}

func TestTrackedSnapshotsAreCopiedAndBounded(t *testing.T) {
	st, _, mid := gatedFixture(t, nil)
	sc, err := st.Create(Schedule{Project: "lab", MachineID: mid, Action: ActionSnapshot, Cron: "@daily", Retain: 1, SnapshotIDs: []string{"injected"}})
	if err != nil || len(sc.SnapshotIDs) != 0 {
		t.Fatalf("caller-supplied history kept: %+v %v", sc, err)
	}
	for i := 0; i < maxTracked+5; i++ {
		if err := st.TrackSnapshot(sc.ID, "s"+strings.Repeat("x", i%3)); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := st.Get(sc.ID)
	if len(got.SnapshotIDs) != maxTracked {
		t.Fatalf("tracked = %d", len(got.SnapshotIDs))
	}
	got.SnapshotIDs[0] = "mutated"
	if again, _ := st.Get(sc.ID); again.SnapshotIDs[0] == "mutated" {
		t.Fatal("Get exposes the stored slice")
	}
}
