// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"bufio"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type bulkResp2 struct {
	DryRun    bool `json:"dryRun"`
	Total     int  `json:"total"`
	Succeeded int  `json:"succeeded"`
	Failed    int  `json:"failed"`
	Results   []struct {
		ID         string `json:"id"`
		OK         bool   `json:"ok"`
		State      string `json:"state"`
		SnapshotID string `json:"snapshotId"`
		Error      *struct {
			Code string `json:"code"`
		} `json:"error"`
	} `json:"results"`
}

func TestListStateAndNameFilters(t *testing.T) {
	e := opsServer(t, opsOpts{})
	a := fleetCreate(t, e, "web-alpha", nil)
	b := fleetCreate(t, e, "web-beta", nil)
	c := fleetCreate(t, e, "db-one", nil)
	mustCode(t, e.do("POST", "/api/v1/machines/"+b+"/stop", tAdmin, nil), http.StatusOK)

	same := func(q string, want ...string) {
		t.Helper()
		got := listIDs(t, e, q)
		if len(got) != len(want) {
			t.Fatalf("%q: got %v want %v", q, got, want)
		}
		set := map[string]bool{}
		for _, g := range got {
			set[g] = true
		}
		for _, w := range want {
			if !set[w] {
				t.Fatalf("%q: got %v want %v", q, got, want)
			}
		}
	}
	same("state=stopped", b)
	same("state=running", a, c)
	same("state=running,stopped", a, b, c)
	same("name=WEB", a, b) // case-insensitive substring
	same("name=WEB&state=running", a)
	same("name=nothing")
	same("state=stopped&selector=env%3Dnone")
	for _, bad := range []string{"state=bogus", "state=running,,", "name=" + strings.Repeat("x", 129)} {
		w := e.do("GET", "/api/v1/machines?"+bad, tAdmin, nil)
		if w.Code != http.StatusBadRequest || errCode(t, w) != "INVALID_REQUEST" {
			t.Errorf("%q: %d %s", bad, w.Code, w.Body.String())
		}
	}
}

func TestAllowedActions(t *testing.T) {
	e := opsServer(t, opsOpts{})
	id := fleetCreate(t, e, "acts", nil)
	get := func() []string {
		w := e.do("GET", "/api/v1/machines/"+id, tAdmin, nil)
		mustCode(t, w, http.StatusOK)
		return decode[struct {
			AllowedActions []string `json:"allowedActions"`
		}](t, w).AllowedActions
	}
	has := func(l []string, a string) bool { return hasAction(l, a) }
	run := get()
	if !has(run, "stop") || has(run, "start") || !has(run, "snapshot") || !has(run, "delete") {
		t.Fatalf("running machine actions = %v", run)
	}
	mustCode(t, e.do("POST", "/api/v1/machines/"+id+"/stop", tAdmin, nil), http.StatusOK)
	stopped := get()
	if !has(stopped, "start") || has(stopped, "stop") {
		t.Fatalf("stopped machine actions = %v", stopped)
	}
	// Also present on list responses.
	w := e.do("GET", "/api/v1/machines", tAdmin, nil)
	mustCode(t, w, http.StatusOK)
	l := decode[struct {
		Items []struct {
			AllowedActions []string `json:"allowedActions"`
		} `json:"items"`
	}](t, w)
	if len(l.Items) != 1 || !has(l.Items[0].AllowedActions, "start") {
		t.Fatalf("list allowedActions: %+v", l.Items)
	}
}

func TestActionsFor(t *testing.T) {
	ac := actionContext{snapshots: true, restart: true}
	if got := strings.Join(ac.actionsFor("running"), ","); got != "stop,restart,snapshot,delete" {
		t.Errorf("running: %s", got)
	}
	if got := strings.Join(actionContext{}.actionsFor("running"), ","); got != "stop,delete" {
		t.Errorf("running without caps: %s", got)
	}
	if got := strings.Join(ac.actionsFor("failed"), ","); got != "start,delete" {
		t.Errorf("failed: %s", got)
	}
	if got := ac.actionsFor("deleting"); len(got) != 0 {
		t.Errorf("deleting: %v", got)
	}
}

func TestBulkSnapshotAndDelete(t *testing.T) {
	e := opsServer(t, opsOpts{})
	a := fleetCreate(t, e, "bs-a", map[string]string{"env": "tmp"})
	b := fleetCreate(t, e, "bs-b", map[string]string{"env": "tmp"})
	c := fleetCreate(t, e, "bs-c", map[string]string{"env": "keep"})

	// Snapshot by selector, with a name.
	w := e.do("POST", "/api/v1/machines/actions", tAdmin, map[string]any{"action": "snapshot", "selector": "env=tmp", "snapshotName": "pre-upgrade"})
	mustCode(t, w, http.StatusOK)
	r := decode[bulkResp2](t, w)
	if r.Total != 2 || r.Succeeded != 2 {
		t.Fatalf("snapshot: %+v", r)
	}
	for _, it := range r.Results {
		if it.SnapshotID == "" || !it.OK {
			t.Fatalf("snapshot result: %+v", it)
		}
	}
	sw := e.do("GET", "/api/v1/machines/"+a+"/snapshots", tAdmin, nil)
	mustCode(t, sw, http.StatusOK)
	if !strings.Contains(sw.Body.String(), "pre-upgrade") {
		t.Fatalf("snapshot name missing: %s", sw.Body.String())
	}

	// Delete without confirm is refused and removes nothing.
	body := map[string]any{"action": "delete", "selector": "env=tmp"}
	mustCode(t, e.do("POST", "/api/v1/machines/actions", tAdmin, body), http.StatusBadRequest)
	if n := len(listIDs(t, e, "")); n != 3 {
		t.Fatalf("machines after refused delete = %d", n)
	}

	// Dry run previews without deleting, flags a machine that cannot be deleted
	// as unknown and never emits events.
	before := len(e.srv.events.List(500))
	w = e.do("POST", "/api/v1/machines/actions", tAdmin, map[string]any{"action": "delete", "ids": []string{a, "ghost"}, "dryRun": true})
	mustCode(t, w, http.StatusOK)
	r = decode[bulkResp2](t, w)
	if !r.DryRun || r.Succeeded != 1 || r.Failed != 1 || r.Results[1].Error == nil || r.Results[1].Error.Code != "NOT_FOUND" {
		t.Fatalf("dry run: %+v", r)
	}
	if n := len(listIDs(t, e, "")); n != 3 {
		t.Fatalf("dry run deleted machines: %d left", n)
	}
	if len(e.srv.events.List(500)) != before {
		t.Fatal("dry run published events")
	}

	// Confirmed delete removes exactly the selected machines and emits events.
	body["confirm"] = true
	w = e.do("POST", "/api/v1/machines/actions", tAdmin, body)
	mustCode(t, w, http.StatusOK)
	r = decode[bulkResp2](t, w)
	if r.Succeeded != 2 || r.Failed != 0 {
		t.Fatalf("delete: %+v", r)
	}
	left := listIDs(t, e, "")
	if len(left) != 1 || left[0] != c {
		t.Fatalf("left %v, want only %s (deleted %s,%s)", left, c, a, b)
	}
	n := 0
	for _, ev := range e.srv.events.List(500) {
		if ev.Type == "io.kryton.machine.deleted" {
			n++
		}
	}
	if n != 2 {
		t.Fatalf("deleted events = %d", n)
	}
}

func TestBulkDryRunStateChecks(t *testing.T) {
	e := opsServer(t, opsOpts{})
	a := fleetCreate(t, e, "dr-a", nil)
	// A running machine cannot be started.
	w := e.do("POST", "/api/v1/machines/actions", tAdmin, map[string]any{"action": "start", "ids": []string{a}, "dryRun": true})
	mustCode(t, w, http.StatusOK)
	r := decode[bulkResp2](t, w)
	if r.Failed != 1 || r.Results[0].Error == nil || r.Results[0].Error.Code != "PRECONDITION_FAILED" || r.Results[0].State != "running" {
		t.Fatalf("dry-run start on running: %+v body=%s", r, w.Body.String())
	}
	// stop is fine, and nothing changed.
	w = e.do("POST", "/api/v1/machines/actions", tAdmin, map[string]any{"action": "stop", "ids": []string{a}, "dryRun": true})
	if r = decode[bulkResp2](t, w); r.Succeeded != 1 {
		t.Fatalf("dry-run stop: %+v", r)
	}
	g := e.do("GET", "/api/v1/machines/"+a, tAdmin, nil)
	if !strings.Contains(g.Body.String(), `"state":"running"`) {
		t.Fatalf("dry run changed state: %s", g.Body.String())
	}
}

func TestBulkNewActionRejects(t *testing.T) {
	e := opsServer(t, opsOpts{})
	bad := map[string]any{
		"snapshotName on stop":  map[string]any{"action": "stop", "ids": []string{"a"}, "snapshotName": "x"},
		"long snapshotName":     map[string]any{"action": "snapshot", "ids": []string{"a"}, "snapshotName": strings.Repeat("x", 129)},
		"delete without ids":    map[string]any{"action": "delete", "confirm": true},
		"unknown action reboot": map[string]any{"action": "reboot", "ids": []string{"a"}},
	}
	for name, body := range bad {
		if w := e.do("POST", "/api/v1/machines/actions", tAdmin, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// Viewers cannot delete in bulk, even as a dry run.
	mustCode(t, e.do("POST", "/api/v1/machines/actions", tViewer, map[string]any{"project": "lab", "action": "delete", "ids": []string{"a"}, "dryRun": true}), http.StatusForbidden)
}

func TestSummaryGroupBy(t *testing.T) {
	e := opsServer(t, opsOpts{})
	fleetCreate(t, e, "g-a", map[string]string{"team": "core"})
	b := fleetCreate(t, e, "g-b", map[string]string{"team": "core"})
	fleetCreate(t, e, "g-c", map[string]string{"team": "web"})
	fleetCreate(t, e, "g-d", nil)
	mustCode(t, e.do("POST", "/api/v1/machines/"+b+"/stop", tAdmin, nil), http.StatusOK)

	w := e.do("GET", "/api/v1/summary?groupBy=team", tAdmin, nil)
	mustCode(t, w, http.StatusOK)
	r := decode[struct {
		Machines int    `json:"machines"`
		GroupBy  string `json:"groupBy"`
		Groups   []struct {
			Value    string `json:"value"`
			Machines int    `json:"machines"`
			Running  int    `json:"running"`
			Stopped  int    `json:"stopped"`
			CPU      int    `json:"cpu"`
		} `json:"groups"`
	}](t, w)
	if r.Machines != 4 || r.GroupBy != "team" || len(r.Groups) != 3 {
		t.Fatalf("summary: %+v", r)
	}
	// Sorted by value: "" (no label), core, web.
	if r.Groups[0].Value != "" || r.Groups[0].Machines != 1 {
		t.Errorf("unlabelled group: %+v", r.Groups[0])
	}
	if g := r.Groups[1]; g.Value != "core" || g.Machines != 2 || g.Running != 1 || g.Stopped != 1 || g.CPU != 8 {
		t.Errorf("core group: %+v", g)
	}
	// Without groupBy the shape is unchanged.
	w = e.do("GET", "/api/v1/summary", tAdmin, nil)
	if strings.Contains(w.Body.String(), "groups") {
		t.Errorf("plain summary has groups: %s", w.Body.String())
	}
	mustCode(t, e.do("GET", "/api/v1/summary?groupBy=a%20b", tAdmin, nil), http.StatusBadRequest)
}

func TestEventStreamFilters(t *testing.T) {
	e := opsServer(t, opsOpts{})
	srv := httptest.NewServer(e.h)
	defer srv.Close()

	open := func(q string) (*http.Response, error) {
		req, _ := http.NewRequest("GET", srv.URL+"/api/v1/events/stream"+q, nil)
		req.Header.Set("Authorization", "Bearer "+tAdmin)
		return srv.Client().Do(req)
	}
	// Invalid filters fail before the stream starts.
	resp, err := open("?type=" + strings.Repeat("x", 200))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad filter status = %d", resp.StatusCode)
	}

	resp, err = open("?type=io.kryton.machine.stopped")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	lines := make(chan string, 64)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "data: ") {
				lines <- sc.Text()
			}
		}
		close(lines)
	}()
	id := fleetCreate(t, e, "stream-a", nil) // created event must be filtered out
	e.srv.events.Publish(context.Background(), "io.kryton.machine.stopped", "machines/"+id, map[string]any{"project": "default", "machineId": id})
	select {
	case l := <-lines:
		if !strings.Contains(l, "io.kryton.machine.stopped") {
			t.Fatalf("unfiltered event leaked: %s", l)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no event received")
	}
	select {
	case l := <-lines:
		t.Fatalf("extra event: %s", l)
	case <-time.After(200 * time.Millisecond):
	}
}

func TestOpenAPICoversFleetV2(t *testing.T) {
	spec := string(openAPISpec)
	for _, want := range []string{"enum: [start, stop, snapshot, delete]", "dryRun:", "confirm:", "snapshotName:", "name: state", "name: groupBy", "allowedActions"} {
		if !strings.Contains(spec, want) {
			t.Errorf("openapi.yaml is missing %q", want)
		}
	}
}
