// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

func fleetBody(name string, labels map[string]string) map[string]any {
	b := map[string]any{"project": "default", "name": name, "image": "windows-server-2025", "compute": map[string]any{"cpu": 4, "memoryMiB": 8192}, "disk": map[string]any{"sizeGiB": 80}}
	if labels != nil {
		b["labels"] = labels
	}
	return b
}

func (e opsEnv) doHeaders(method, path, token string, hdr map[string]string, body any) *httptest.ResponseRecorder {
	var rd io.Reader
	switch b := body.(type) {
	case nil:
	case string:
		rd = strings.NewReader(b)
	default:
		j, _ := json.Marshal(b)
		rd = bytes.NewReader(j)
	}
	r := httptest.NewRequest(method, path, rd)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	for k, v := range hdr {
		r.Header[http.CanonicalHeaderKey(k)] = []string{v}
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func fleetCreate(t *testing.T, e opsEnv, name string, labels map[string]string) string {
	t.Helper()
	w := e.do("POST", "/api/v1/machines", tAdmin, fleetBody(name, labels))
	mustCode(t, w, http.StatusCreated)
	return decode[struct {
		ID string `json:"id"`
	}](t, w).ID
}

func listIDs(t *testing.T, e opsEnv, query string) []string {
	t.Helper()
	w := e.do("GET", "/api/v1/machines?"+query, tAdmin, nil)
	mustCode(t, w, http.StatusOK)
	r := decode[struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}](t, w)
	var ids []string
	for _, i := range r.Items {
		ids = append(ids, i.ID)
	}
	return ids
}

func TestListSelector(t *testing.T) {
	e := opsServer(t, opsOpts{})
	a := fleetCreate(t, e, "sel-a", map[string]string{"env": "ci", "team": "core"})
	b := fleetCreate(t, e, "sel-b", map[string]string{"env": "prod"})
	c := fleetCreate(t, e, "sel-c", nil)
	cases := []struct {
		sel  string
		want []string
	}{
		{"env=ci", []string{a}},
		{"env in (ci,prod)", []string{a, b}},
		{"env!=ci", []string{b, c}},
		{"!env", []string{c}},
		{"team", []string{a}},
		{"env=ci,team=other", nil},
		{"", []string{a, b, c}},
	}
	for _, tc := range cases {
		got := listIDs(t, e, "selector="+url.QueryEscape(tc.sel))
		if len(got) != len(tc.want) {
			t.Errorf("selector %q: got %d machines want %d", tc.sel, len(got), len(tc.want))
			continue
		}
		set := map[string]bool{}
		for _, g := range got {
			set[g] = true
		}
		for _, w := range tc.want {
			if !set[w] {
				t.Errorf("selector %q: missing %s", tc.sel, w)
			}
		}
	}
	// Pagination applies to the filtered set.
	w := e.do("GET", "/api/v1/machines?limit=1&selector="+url.QueryEscape("env in (ci,prod)"), tAdmin, nil)
	mustCode(t, w, http.StatusOK)
	if decode[struct {
		NextCursor string `json:"nextCursor"`
	}](t, w).NextCursor == "" {
		t.Error("expected a next cursor over the filtered set")
	}
}

func TestListSelectorHostile(t *testing.T) {
	e := opsServer(t, opsOpts{})
	bad := []string{
		"a in (b", "a=b,,c", strings.Repeat("a=b,", 40) + "c=d", "a=" + strings.Repeat("x", 2000),
		"a in ((b))", "k\x00=v", "a b=c", "in (x)", "a=b)", "!", "a in ()",
	}
	for _, s := range bad {
		w := e.do("GET", "/api/v1/machines?selector="+url.QueryEscape(s), tAdmin, nil)
		if w.Code != http.StatusBadRequest || errCode(t, w) != "INVALID_REQUEST" {
			t.Errorf("selector %q: status=%d body=%s", s, w.Code, w.Body.String())
		}
	}
}

func TestCreateLabelsCapped(t *testing.T) {
	e := opsServer(t, opsOpts{})
	labels := map[string]string{}
	for i := 0; i < 65; i++ {
		labels[fmt.Sprintf("k%d", i)] = "v"
	}
	w := e.do("POST", "/api/v1/machines", tAdmin, fleetBody("too-many", labels))
	mustCode(t, w, http.StatusBadRequest)
}

func TestIdempotentCreate(t *testing.T) {
	e := opsServer(t, opsOpts{})
	h := map[string]string{"Idempotency-Key": "order-1234"}
	w1 := e.doHeaders("POST", "/api/v1/machines", tAdmin, h, fleetBody("idem-a", nil))
	mustCode(t, w1, http.StatusCreated)
	if w1.Header().Get("Idempotent-Replay") != "" {
		t.Fatal("first response must not be marked as a replay")
	}
	w2 := e.doHeaders("POST", "/api/v1/machines", tAdmin, h, fleetBody("idem-a", nil))
	mustCode(t, w2, http.StatusCreated)
	if w2.Header().Get("Idempotent-Replay") != "true" {
		t.Fatal("retry must be marked Idempotent-Replay: true")
	}
	id1 := decode[struct{ ID string }](t, w1).ID
	id2 := decode[struct{ ID string }](t, w2).ID
	if id1 == "" || id1 != id2 {
		t.Fatalf("replay returned a different machine: %q vs %q", id1, id2)
	}
	if n := len(listIDs(t, e, "")); n != 1 {
		t.Fatalf("retry created %d machines, want 1", n)
	}
	// Quoted Structured Field form is the same key.
	w3 := e.doHeaders("POST", "/api/v1/machines", tAdmin, map[string]string{"Idempotency-Key": `"order-1234"`}, fleetBody("idem-a", nil))
	if w3.Header().Get("Idempotent-Replay") != "true" {
		t.Fatalf("quoted key should replay: %d %s", w3.Code, w3.Body.String())
	}
	// Same key, different body: 422.
	w4 := e.doHeaders("POST", "/api/v1/machines", tAdmin, h, fleetBody("idem-b", nil))
	mustCode(t, w4, http.StatusUnprocessableEntity)
	if errCode(t, w4) != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("code=%s", errCode(t, w4))
	}
	// A different caller with the same key is independent.
	w5 := e.doHeaders("POST", "/api/v1/machines", tMixed, map[string]string{"Idempotency-Key": "order-1234"}, map[string]any{"project": "lab", "name": "idem-a", "image": "windows-server-2025", "compute": map[string]any{"cpu": 4, "memoryMiB": 8192}, "disk": map[string]any{"sizeGiB": 80}})
	mustCode(t, w5, http.StatusCreated)
	if w5.Header().Get("Idempotent-Replay") != "" {
		t.Fatal("another caller must not see someone else's replay")
	}
}

func TestIdempotencyKeyValidation(t *testing.T) {
	e := opsServer(t, opsOpts{})
	for name, key := range map[string]string{
		"too long":   strings.Repeat("k", 129),
		"space":      "a b",
		"slash":      "a/b",
		"unicode":    "kéy",
		"empty":      "",
		"semicolon":  "a;b",
		"half quote": `"abc`,
	} {
		w := e.doHeaders("POST", "/api/v1/machines", tAdmin, map[string]string{"Idempotency-Key": key}, fleetBody("v-"+strings.ReplaceAll(name, " ", "-"), nil))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s", name, w.Code, w.Body.String())
		}
	}
	if n := len(listIDs(t, e, "")); n != 0 {
		t.Fatalf("invalid keys created %d machines", n)
	}
	// 128 characters is the maximum accepted.
	w := e.doHeaders("POST", "/api/v1/machines", tAdmin, map[string]string{"Idempotency-Key": strings.Repeat("k", 128)}, fleetBody("max-key", nil))
	mustCode(t, w, http.StatusCreated)
}

func TestIdempotentFailureIsNotRemembered(t *testing.T) {
	e := opsServer(t, opsOpts{})
	fleetCreate(t, e, "taken", nil)
	h := map[string]string{"Idempotency-Key": "retry-me"}
	for i := 0; i < 2; i++ {
		w := e.doHeaders("POST", "/api/v1/machines", tAdmin, h, fleetBody("taken", nil))
		mustCode(t, w, http.StatusConflict)
		if w.Header().Get("Idempotent-Replay") != "" {
			t.Fatal("a failed create must not be replayed")
		}
		if errCode(t, w) != "CONFLICT" {
			t.Fatalf("code=%s", errCode(t, w))
		}
	}
}

func TestIdemStoreLifecycle(t *testing.T) {
	now := time.Unix(1000, 0)
	s := newIdemStore()
	s.now = func() time.Time { return now }
	if st, _ := s.begin("k", "fp"); st != idemNew {
		t.Fatalf("first begin = %v", st)
	}
	if st, _ := s.begin("k", "fp"); st != idemInFlight {
		t.Fatalf("second begin before completion = %v", st)
	}
	if st, _ := s.begin("k", "other"); st != idemMismatch {
		t.Fatalf("different fingerprint = %v", st)
	}
	s.complete("k", 201, []byte("x"))
	if st, e := s.begin("k", "fp"); st != idemReplay || string(e.body) != "x" {
		t.Fatalf("after complete = %v", st)
	}
	now = now.Add(idemTTL + time.Second)
	if st, _ := s.begin("k", "fp"); st != idemNew {
		t.Fatalf("expired key should start over, got %v", st)
	}
	s.abandon("k")
	if st, _ := s.begin("k", "fp"); st != idemNew {
		t.Fatalf("abandoned key should start over, got %v", st)
	}
	// Capacity: live entries are never evicted.
	s2 := newIdemStore()
	for i := 0; i < idemMaxEntries; i++ {
		s2.m[fmt.Sprint(i)] = &idemEntry{expires: time.Now().Add(time.Hour)}
	}
	if st, _ := s2.begin("new", "fp"); st != idemFull {
		t.Fatalf("full store = %v", st)
	}
	for _, e := range s2.m {
		e.expires = time.Now().Add(-time.Hour)
	}
	if st, _ := s2.begin("new", "fp"); st != idemNew {
		t.Fatalf("expired entries should be reclaimed, got %v", st)
	}
}

type bulkResp struct {
	Total     int `json:"total"`
	Succeeded int `json:"succeeded"`
	Failed    int `json:"failed"`
	Results   []struct {
		ID    string `json:"id"`
		OK    bool   `json:"ok"`
		State string `json:"state"`
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	} `json:"results"`
}

func TestBulkActions(t *testing.T) {
	e := opsServer(t, opsOpts{})
	a := fleetCreate(t, e, "bulk-a", map[string]string{"env": "ci"})
	b := fleetCreate(t, e, "bulk-b", map[string]string{"env": "ci"})
	c := fleetCreate(t, e, "bulk-c", map[string]string{"env": "prod"})

	// By selector.
	w := e.do("POST", "/api/v1/machines/actions", tAdmin, map[string]any{"action": "stop", "selector": "env=ci"})
	mustCode(t, w, http.StatusOK)
	r := decode[bulkResp](t, w)
	if r.Total != 2 || r.Succeeded != 2 || r.Failed != 0 {
		t.Fatalf("selector stop: %+v", r)
	}
	for _, it := range r.Results {
		if !it.OK || it.State != "stopped" || (it.ID != a && it.ID != b) {
			t.Fatalf("unexpected result %+v", it)
		}
	}
	// prod machine untouched.
	g := e.do("GET", "/api/v1/machines/"+c, tAdmin, nil)
	if decode[struct{ State string }](t, g).State == "stopped" {
		t.Fatal("selector stopped a machine it should not match")
	}
	// By ids, with one unknown and one duplicate: per-item results, 200.
	w = e.do("POST", "/api/v1/machines/actions", tAdmin, map[string]any{"action": "start", "ids": []string{a, a, "no-such-id"}})
	mustCode(t, w, http.StatusOK)
	r = decode[bulkResp](t, w)
	if r.Total != 2 || r.Succeeded != 1 || r.Failed != 1 {
		t.Fatalf("ids start: %+v", r)
	}
	if r.Results[1].Error == nil || r.Results[1].Error.Code != "NOT_FOUND" {
		t.Fatalf("unknown id result: %+v", r.Results[1])
	}
	// Events were published per machine.
	n := 0
	for _, ev := range e.srv.events.List(100) {
		if ev.Type == "io.kryton.machine.stopped" || ev.Type == "io.kryton.machine.started" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("published %d lifecycle events, want 3", n)
	}
	// A selector that matches nothing is a valid empty batch.
	w = e.do("POST", "/api/v1/machines/actions", tAdmin, map[string]any{"action": "stop", "selector": "env=none"})
	mustCode(t, w, http.StatusOK)
	if r := decode[bulkResp](t, w); r.Total != 0 || len(r.Results) != 0 {
		t.Fatalf("empty batch: %+v", r)
	}
}

func TestBulkActionsRejects(t *testing.T) {
	e := opsServer(t, opsOpts{})
	many := make([]string, 101)
	for i := range many {
		many[i] = fmt.Sprintf("id-%d", i)
	}
	bad := map[string]any{
		"unknown action":  map[string]any{"action": "reboot", "ids": []string{"a"}},
		"missing action":  map[string]any{"ids": []string{"a"}},
		"neither":         map[string]any{"action": "stop"},
		"both":            map[string]any{"action": "stop", "ids": []string{"a"}, "selector": "env=ci"},
		"101 ids":         map[string]any{"action": "stop", "ids": many},
		"path-ish id":     map[string]any{"action": "stop", "ids": []string{"../x"}},
		"long id":         map[string]any{"action": "stop", "ids": []string{strings.Repeat("a", 65)}},
		"bad selector":    map[string]any{"action": "stop", "selector": "a in (b"},
		"unknown project": map[string]any{"action": "stop", "ids": []string{"a"}, "project": "nope"},
		"unknown field":   map[string]any{"action": "stop", "ids": []string{"a"}, "force": true},
	}
	for name, body := range bad {
		if w := e.do("POST", "/api/v1/machines/actions", tAdmin, body); w.Code != http.StatusBadRequest {
			t.Errorf("%s: status=%d body=%s", name, w.Code, w.Body.String())
		}
	}
	if w := e.do("POST", "/api/v1/machines/actions", tAdmin, "{not json"); w.Code != http.StatusBadRequest {
		t.Errorf("malformed json: %d", w.Code)
	}
	// Viewers cannot run bulk actions; no token is unauthorized.
	ok := map[string]any{"project": "lab", "action": "stop", "ids": []string{"a"}}
	mustCode(t, e.do("POST", "/api/v1/machines/actions", tViewer, ok), http.StatusForbidden)
	mustCode(t, e.do("POST", "/api/v1/machines/actions", "", ok), http.StatusUnauthorized)
}

func TestEventFilters(t *testing.T) {
	e := opsServer(t, opsOpts{})
	a := fleetCreate(t, e, "ev-a", nil)
	b := fleetCreate(t, e, "ev-b", nil)
	mustCode(t, e.do("POST", "/api/v1/machines/"+a+"/stop", tAdmin, nil), http.StatusOK)
	count := func(q string) int {
		w := e.do("GET", "/api/v1/events?"+q, tAdmin, nil)
		mustCode(t, w, http.StatusOK)
		return len(decode[struct {
			Items []any `json:"items"`
		}](t, w).Items)
	}
	cases := []struct {
		q    string
		want int
	}{
		{"", 3},
		{"type=io.kryton.machine.created", 2},
		{"type=io.kryton.machine.stopped", 1},
		{"type=" + url.QueryEscape("io.kryton.machine.*"), 3},
		{"type=io.kryton.machine.created,io.kryton.machine.stopped", 3},
		{"machineId=" + a, 2},
		{"machineId=" + b, 1},
		{"machineId=" + a + "&type=io.kryton.machine.stopped", 1},
		{"type=nothing", 0},
		{"since=" + url.QueryEscape(time.Now().Add(time.Hour).UTC().Format(time.RFC3339)), 0},
		{"since=" + url.QueryEscape(time.Now().Add(-time.Hour).UTC().Format(time.RFC3339)), 3},
		{"limit=1&type=" + url.QueryEscape("io.kryton.machine.*"), 1},
	}
	for _, c := range cases {
		if got := count(c.q); got != c.want {
			t.Errorf("%q: got %d want %d", c.q, got, c.want)
		}
	}
	for _, q := range []string{"since=yesterday", "machineId=../x", "type=a%20b", "type=" + strings.Repeat("a", 129), "type=a,b,c,d,e,f,g,h,i", "type=,"} {
		if w := e.do("GET", "/api/v1/events?"+q, tAdmin, nil); w.Code != http.StatusBadRequest {
			t.Errorf("%q: status=%d", q, w.Code)
		}
	}
}

// TestOpenAPICoversFleetEndpoints keeps openapi.yaml in sync with the
// selector, idempotency, bulk and event-filter surface, and the three
// shipped copies of the spec identical.
func TestOpenAPICoversFleetEndpoints(t *testing.T) {
	spec := string(openAPISpec)
	for _, want := range []string{
		"\n  /api/v1/machines/actions:\n    post:",
		"name: selector",
		"name: Idempotency-Key",
		"IDEMPOTENCY_KEY_REUSED",
		"name: machineId",
		"name: since",
		"\n        labels:\n",
	} {
		if !strings.Contains(spec, want) {
			t.Errorf("openapi.yaml is missing %q", want)
		}
	}
	root, err := os.ReadFile("../../openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	cmdCopy, err := os.ReadFile("../../cmd/krytond/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if string(root) != spec || string(cmdCopy) != spec {
		t.Error("openapi.yaml copies differ: copy the root file to cmd/krytond and internal/api")
	}
}
