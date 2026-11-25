// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zyvorai/kryton/internal/audit"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

type protResp struct {
	Protected  bool                     `json:"protected"`
	Protection *model.MachineProtection `json:"protection"`
}

func protect3(t *testing.T, e opsEnv, token, id string, body any) int {
	t.Helper()
	return e.do("PUT", "/api/v1/machines/"+id+"/protection", token, body).Code
}

func getMachine3(t *testing.T, e opsEnv, id string) model.Machine {
	t.Helper()
	w := e.do("GET", "/api/v1/machines/"+id, tAdmin, nil)
	mustCode(t, w, http.StatusOK)
	return decode[model.Machine](t, w)
}

func TestDeleteProtectionBlocksEveryDeletePath(t *testing.T) {
	e := opsServer(t, opsOpts{})
	id := fleetCreate(t, e, "keep", map[string]string{"env": "p"})
	other := fleetCreate(t, e, "scratch", map[string]string{"env": "p"})

	w := e.do("PUT", "/api/v1/machines/"+id+"/protection", tAdmin, map[string]any{"protected": true, "reason": "prod db"})
	mustCode(t, w, http.StatusOK)
	if p := decode[protResp](t, w); !p.Protected || p.Protection == nil || p.Protection.Reason != "prod db" || p.Protection.SetBy != "legacy" {
		t.Fatalf("response = %+v", p)
	}
	m := getMachine3(t, e, id)
	if m.Protection == nil || hasAction(m.AllowedActions, "delete") || !hasAction(m.AllowedActions, "stop") {
		t.Fatalf("machine = protection %v actions %v", m.Protection, m.AllowedActions)
	}
	list := e.do("GET", "/api/v1/machines", tAdmin, nil)
	if !strings.Contains(list.Body.String(), `"reason":"prod db"`) {
		t.Fatalf("list lacks protection: %s", list.Body.String())
	}

	del := e.do("DELETE", "/api/v1/machines/"+id, tAdmin, nil)
	if del.Code != http.StatusConflict || errCode(t, del) != "MACHINE_PROTECTED" || !strings.Contains(del.Body.String(), "prod db") {
		t.Fatalf("delete = %d %s", del.Code, del.Body.String())
	}

	dw := e.do("POST", "/api/v1/machines/actions", tAdmin, map[string]any{"action": "delete", "selector": "env=p", "dryRun": true})
	mustCode(t, dw, http.StatusOK)
	dry := decode[bulkResp2](t, dw)
	if dry.Succeeded != 1 || dry.Failed != 1 {
		t.Fatalf("dry run = %+v", dry)
	}
	for _, res := range dry.Results {
		if (res.ID == id) == res.OK || (res.ID == id && res.Error.Code != "MACHINE_PROTECTED") {
			t.Fatalf("dry run result %+v", res)
		}
	}
	w = e.do("POST", "/api/v1/machines/actions", tAdmin, map[string]any{"action": "delete", "ids": []string{id, other}, "confirm": true})
	mustCode(t, w, http.StatusOK)
	r := decode[bulkResp2](t, w)
	if r.Succeeded != 1 || r.Failed != 1 || r.Results[0].Error == nil || r.Results[0].Error.Code != "MACHINE_PROTECTED" || r.Results[0].State == "deleting" {
		t.Fatalf("bulk delete = %+v", r)
	}
	getMachine3(t, e, id) // still there
	mustCode(t, e.do("GET", "/api/v1/machines/"+other, tAdmin, nil), http.StatusNotFound)

	// Stop and start are unaffected.
	mustCode(t, e.do("POST", "/api/v1/machines/"+id+"/stop", tAdmin, nil), http.StatusOK)

	if c := protect3(t, e, tAdmin, id, map[string]any{"protected": false}); c != http.StatusOK {
		t.Fatalf("unprotect = %d", c)
	}
	if m := getMachine3(t, e, id); m.Protection != nil || !hasAction(m.AllowedActions, "delete") {
		t.Fatalf("after unprotect: %+v %v", m.Protection, m.AllowedActions)
	}
	mustCode(t, e.do("DELETE", "/api/v1/machines/"+id, tAdmin, nil), http.StatusNoContent)

	var actions []string
	entries, _, err := e.audit.Query(audit.Filter{Action: "machine.protection.update", Result: audit.ResultSuccess})
	if err != nil {
		t.Fatal(err)
	}
	for _, en := range entries {
		actions = append(actions, en.Detail)
	}
	if len(actions) != 2 {
		t.Fatalf("audit entries = %v", actions)
	}
}

func TestDeleteProtectionValidationAndRoles(t *testing.T) {
	e := opsServer(t, opsOpts{})
	id := createMachineID(t, e, tAdmin, "lab", "guarded", 4)
	url := "/api/v1/machines/" + id + "/protection?project=lab"
	for name, body := range map[string]any{
		"empty":            map[string]any{},
		"reason only":      map[string]any{"reason": "x"},
		"unknown field":    map[string]any{"protected": true, "force": true},
		"reason on remove": map[string]any{"protected": false, "reason": "x"},
		"long reason":      map[string]any{"protected": true, "reason": strings.Repeat("x", 257)},
	} {
		w := e.do("PUT", url, tAdmin, body)
		if w.Code != http.StatusBadRequest || errCode(t, w) != "INVALID_REQUEST" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	mustCode(t, e.do("PUT", url, tViewer, map[string]any{"protected": true}), http.StatusForbidden)
	mustCode(t, e.do("PUT", url, tLabOp, map[string]any{"protected": true}), http.StatusOK)
	mustCode(t, e.do("PUT", url, tViewer, map[string]any{"protected": false}), http.StatusForbidden)
	mustCode(t, e.do("PUT", "/api/v1/machines/missing/protection", tAdmin, map[string]any{"protected": true}), http.StatusNotFound)
	// Another project's ID is not found through this project.
	mustCode(t, e.do("PUT", "/api/v1/machines/"+id+"/protection?project=default", tAdmin, map[string]any{"protected": false}), http.StatusNotFound)
}

// flakySnapshots fails or relabels snapshot operations on demand.
type flakySnapshots struct {
	provider.Provider
	mu          sync.Mutex
	failTake    bool
	failDelete  bool
	failList    string // machine ID whose ListSnapshots fails
	state       map[string]string
	nextPending bool
}

func (f *flakySnapshots) Snapshot(ctx context.Context, p, m, name string) (*model.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failTake {
		return nil, errors.New("backend down")
	}
	s, err := f.Provider.Snapshot(ctx, p, m, name)
	if err == nil && f.nextPending {
		if f.state == nil {
			f.state = map[string]string{}
		}
		f.state[s.ID] = "creating"
	}
	return s, err
}

func (f *flakySnapshots) ListSnapshots(ctx context.Context, p, m string) ([]model.Snapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failList == m {
		return nil, errors.New("list failed")
	}
	out, err := f.Provider.ListSnapshots(ctx, p, m)
	for i := range out {
		if st, ok := f.state[out[i].ID]; ok {
			out[i].State = st
		}
	}
	return out, err
}

func (f *flakySnapshots) DeleteSnapshot(ctx context.Context, p, m, s string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failDelete {
		return errors.New("delete refused")
	}
	return f.Provider.DeleteSnapshot(ctx, p, m, s)
}

type schedResp struct {
	ID                  string   `json:"id"`
	Enabled             bool     `json:"enabled"`
	Retain              int      `json:"retain"`
	MaxFailures         int      `json:"maxFailures"`
	SnapshotIDs         []string `json:"snapshotIds"`
	LastResult          string   `json:"lastResult"`
	LastError           string   `json:"lastError"`
	RunCount            int      `json:"runCount"`
	NextRunAt           string   `json:"nextRunAt"`
	ConsecutiveFailures int      `json:"consecutiveFailures"`
}

func snapIDs3(t *testing.T, e opsEnv, machine string) map[string]bool {
	t.Helper()
	w := e.do("GET", "/api/v1/machines/"+machine+"/snapshots", tAdmin, nil)
	mustCode(t, w, http.StatusOK)
	out := map[string]bool{}
	for _, s := range decode[listResponse[model.Snapshot]](t, w).Items {
		out[s.ID] = true
	}
	return out
}

func TestScheduleRunNowAppliesRetention(t *testing.T) {
	var flaky *flakySnapshots
	e := opsServer(t, opsOpts{wrap: func(p provider.Provider) provider.Provider {
		flaky = &flakySnapshots{Provider: p}
		return flaky
	}})
	id := fleetCreate(t, e, "retained", nil)
	// A manual snapshot must survive retention.
	mw := e.do("POST", "/api/v1/machines/"+id+"/snapshot", tAdmin, map[string]any{"name": "manual"})
	mustCode(t, mw, http.StatusCreated)
	manual := decode[model.Snapshot](t, mw).ID

	w := e.do("POST", "/api/v1/schedules", tAdmin, map[string]any{"machineId": id, "action": "snapshot", "cron": "0 3 * * *", "retain": 2, "snapshotName": "nightly"})
	mustCode(t, w, http.StatusCreated)
	sc := decode[schedResp](t, w)
	if sc.Retain != 2 || len(sc.SnapshotIDs) != 0 {
		t.Fatalf("created = %+v", sc)
	}
	run := func() schedResp {
		w := e.do("POST", "/api/v1/schedules/"+sc.ID+"/run", tAdmin, nil)
		mustCode(t, w, http.StatusOK)
		return decode[schedResp](t, w)
	}
	first := run()
	if first.LastResult != "success" || first.RunCount != 1 || len(first.SnapshotIDs) != 1 || first.NextRunAt != sc.NextRunAt {
		t.Fatalf("first run = %+v (next was %s)", first, sc.NextRunAt)
	}
	second := run()
	third := run()
	if len(third.SnapshotIDs) != 2 || third.SnapshotIDs[0] != second.SnapshotIDs[1] {
		t.Fatalf("after three runs tracked = %v (second %v)", third.SnapshotIDs, second.SnapshotIDs)
	}
	have := snapIDs3(t, e, id)
	if len(have) != 3 || !have[manual] || have[first.SnapshotIDs[0]] || !have[third.SnapshotIDs[0]] || !have[third.SnapshotIDs[1]] {
		t.Fatalf("snapshots = %v, tracked %v", have, third.SnapshotIDs)
	}
	names := e.do("GET", "/api/v1/machines/"+id+"/snapshots", tAdmin, nil).Body.String()
	if !strings.Contains(names, `"nightly-2`) {
		t.Fatalf("retained snapshots need distinct names: %s", names)
	}

	// While new snapshots are not usable yet, the old good ones stay.
	flaky.nextPending = true
	run()
	pending := run()
	if len(pending.SnapshotIDs) != 4 || len(snapIDs3(t, e, id)) != 5 {
		t.Fatalf("pending snapshots caused deletion: %v", pending.SnapshotIDs)
	}
	flaky.nextPending, flaky.state = false, nil

	// A delete failure is reported, kept tracked, and retried.
	flaky.failDelete = true
	failed := run()
	if failed.LastResult != "success" || !strings.Contains(failed.LastError, "retention:") || len(failed.SnapshotIDs) != 5 {
		t.Fatalf("delete failure = %+v", failed)
	}
	flaky.failDelete = false
	// A tracked snapshot removed by hand is forgotten.
	mustCode(t, e.do("DELETE", "/api/v1/machines/"+id+"/snapshots/"+failed.SnapshotIDs[4], tAdmin, nil), http.StatusNoContent)
	clean := run()
	if clean.LastError != "" || len(clean.SnapshotIDs) != 2 {
		t.Fatalf("after retry = %+v", clean)
	}
	if have := snapIDs3(t, e, id); len(have) != 3 || !have[manual] {
		t.Fatalf("final snapshots = %v", have)
	}
	ev := e.do("GET", "/api/v1/events?type=io.kryton.snapshot.deleted&limit=100", tAdmin, nil).Body.String()
	if !strings.Contains(ev, "schedule-retention") {
		t.Fatalf("no retention event: %s", ev)
	}
}

func TestScheduleRunNowErrorsAndRoles(t *testing.T) {
	var flaky *flakySnapshots
	e := opsServer(t, opsOpts{wrap: func(p provider.Provider) provider.Provider {
		flaky = &flakySnapshots{Provider: p}
		return flaky
	}})
	id := createMachineID(t, e, tAdmin, "lab", "runme", 4)
	w := e.do("POST", "/api/v1/schedules", tAdmin, map[string]any{"project": "lab", "machineId": id, "action": "snapshot", "cron": "@daily", "maxFailures": 1})
	mustCode(t, w, http.StatusCreated)
	sc := decode[schedResp](t, w)
	url := "/api/v1/schedules/" + sc.ID
	mustCode(t, e.do("POST", url+"/run", tViewer, nil), http.StatusNotFound)
	mustCode(t, e.do("POST", "/api/v1/schedules/nope/run", tAdmin, nil), http.StatusNotFound)
	flaky.failTake = true
	if w := e.do("POST", url+"/run", tLabOp, nil); w.Code < 400 {
		t.Fatalf("failed action returned %d", w.Code)
	}
	got := decode[schedResp](t, e.do("GET", url, tAdmin, nil))
	if got.LastResult != "error" || !got.Enabled || got.ConsecutiveFailures != 0 {
		t.Fatalf("a manual failure must not disable: %+v", got)
	}
	for _, bad := range []map[string]any{
		{"machineId": id, "action": "stop", "cron": "@daily", "retain": 1},
		{"machineId": id, "action": "snapshot", "cron": "@daily", "retain": 101},
		{"machineId": id, "action": "snapshot", "cron": "@daily", "retain": -1},
		{"machineId": id, "action": "snapshot", "cron": "@daily", "maxFailures": -1},
		{"machineId": id, "action": "snapshot", "cron": "@daily", "snapshotIds": []string{"x"}},
	} {
		bad["project"] = "lab"
		mustCode(t, e.do("POST", "/api/v1/schedules", tAdmin, bad), http.StatusBadRequest)
	}
	mustCode(t, e.do("PUT", url, tAdmin, map[string]any{"retain": 3}), http.StatusOK)
	// Changing the action away from snapshot while retain is set is refused.
	mustCode(t, e.do("PUT", url, tAdmin, map[string]any{"action": "stop"}), http.StatusBadRequest)
	mustCode(t, e.do("PUT", url, tAdmin, map[string]any{"action": "stop", "retain": 0}), http.StatusOK)
}

func TestSnapshotInventory(t *testing.T) {
	var flaky *flakySnapshots
	e := opsServer(t, opsOpts{wrap: func(p provider.Provider) provider.Provider {
		flaky = &flakySnapshots{Provider: p}
		return flaky
	}})
	a := fleetCreate(t, e, "inv-a", map[string]string{"env": "ci"})
	b := fleetCreate(t, e, "inv-b", nil)
	for _, m := range []string{a, a, b} {
		mustCode(t, e.do("POST", "/api/v1/machines/"+m+"/snapshot", tAdmin, nil), http.StatusCreated)
		time.Sleep(2 * time.Millisecond)
	}
	w := e.do("POST", "/api/v1/schedules", tAdmin, map[string]any{"machineId": b, "action": "snapshot", "cron": "@daily", "retain": 5})
	mustCode(t, w, http.StatusCreated)
	sid := decode[schedResp](t, w).ID
	mustCode(t, e.do("POST", "/api/v1/schedules/"+sid+"/run", tAdmin, nil), http.StatusOK)

	get := func(q string) snapshotInventory {
		w := e.do("GET", "/api/v1/snapshots"+q, tAdmin, nil)
		mustCode(t, w, http.StatusOK)
		return decode[snapshotInventory](t, w)
	}
	all := get("")
	if all.Total != 4 || len(all.Items) != 4 || all.Machines != 2 || len(all.Errors) != 0 {
		t.Fatalf("all = %+v", all)
	}
	for i := 1; i < len(all.Items); i++ {
		if all.Items[i].CreatedAt.Before(all.Items[i-1].CreatedAt) {
			t.Fatalf("not oldest first: %+v", all.Items)
		}
	}
	if all.Items[0].MachineName != "inv-a" || all.Items[0].ScheduleID != "" || all.Items[3].ScheduleID != sid {
		t.Fatalf("annotations: %+v", all.Items)
	}
	if p := get("?limit=1"); p.Total != 4 || len(p.Items) != 1 || p.Items[0].ID != all.Items[0].ID {
		t.Fatalf("limit = %+v", p)
	}
	if s := get("?selector=env%3Dci"); s.Total != 2 || s.Machines != 1 {
		t.Fatalf("selector = %+v", s)
	}
	if o := get("?olderThan=1d"); o.Total != 0 || len(o.Items) != 0 {
		t.Fatalf("olderThan=1d = %+v", o)
	}
	if o := get("?olderThan=1ns"); o.Total != 4 {
		t.Fatalf("olderThan=1ns = %+v", o)
	}
	flaky.failList = a
	if p := get(""); p.Total != 2 || len(p.Errors) != 1 || p.Errors[0].MachineID != a || p.Errors[0].Error.Code == "" {
		t.Fatalf("partial = %+v", p)
	}
	for _, bad := range []string{"olderThan=0d", "olderThan=-1h", "olderThan=soon", "olderThan=99999d", "limit=0", "limit=501", "limit=x", "selector=%3D%3D", "project=nope"} {
		mustCode(t, e.do("GET", "/api/v1/snapshots?"+bad, tAdmin, nil), http.StatusBadRequest)
	}
	mustCode(t, e.do("GET", "/api/v1/snapshots?project=default", tViewer, nil), http.StatusForbidden)
}

// noSnapshots mimics dockur and libvirt.
type noSnapshots struct{ provider.Provider }

func (n noSnapshots) Capabilities(ctx context.Context) (model.Capabilities, error) {
	c, err := n.Provider.Capabilities(ctx)
	c.Snapshots = false
	return c, err
}

func TestSnapshotInventoryUnsupportedProvider(t *testing.T) {
	e := opsServer(t, opsOpts{wrap: func(p provider.Provider) provider.Provider { return noSnapshots{p} }})
	w := e.do("GET", "/api/v1/snapshots", tAdmin, nil)
	if w.Code != http.StatusNotImplemented || errCode(t, w) != "UNSUPPORTED" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
}

// Every documented endpoint must have its own metrics/audit route label, so
// audit actions and latency histograms do not lump routes together.
func TestEveryEndpointHasARouteLabel(t *testing.T) {
	for _, ep := range allAPIEndpoints() {
		if !strings.HasPrefix(ep.Path, "/api/") {
			continue
		}
		concrete := ep.Path
		for _, p := range []string{"{id}", "{sid}", "{jid}"} {
			concrete = strings.ReplaceAll(concrete, p, "abc123")
		}
		concrete = strings.ReplaceAll(concrete, "{project}", "lab")
		concrete = strings.ReplaceAll(concrete, "{path}", "dir/sub/file.txt")
		if got := routeLabel(concrete); got != ep.Path {
			t.Errorf("routeLabel(%s) = %s, want %s", concrete, got, ep.Path)
		}
	}
	if got := auditActionFor("POST", "/api/v1/machines/actions"); got != "machine.bulk" {
		t.Errorf("bulk audit action = %s", got)
	}
}
