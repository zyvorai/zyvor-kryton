// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zyvorai/kryton/internal/audit"
	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/demo"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/metrics"
	"github.com/zyvorai/kryton/internal/quota"
	"github.com/zyvorai/kryton/internal/schedule"
	"github.com/zyvorai/kryton/internal/webhooks"
)

type opsOpts struct {
	disabledAuth  bool
	limits        quota.Limits
	rps           int
	metricsToken  string
	metricsPublic bool
	defaultTTL    int
}

type opsEnv struct {
	h     http.Handler
	srv   *Server
	audit *audit.Store
	hooks *webhooks.Manager
}

const (
	tAdmin    = "tok-admin"   // legacy key: no role field => admin on everything
	tViewer   = "tok-viewer"  // viewer on lab
	tLabOp    = "tok-lab-op"  // operator on lab only (grants)
	tMixed    = "tok-mixed"   // viewer everywhere, operator on lab, admin on default
	tLabAdmin = "tok-lab-adm" // admin on lab only
)

func opsServer(t *testing.T, o opsOpts) opsEnv {
	t.Helper()
	cat, err := catalog.Load("")
	if err != nil {
		t.Fatal(err)
	}
	mode := "apikey"
	if o.disabledAuth {
		mode = "disabled"
	}
	keys := fmt.Sprintf(`{"keys":[
		{"name":"legacy","sha256":%q,"projects":["*"]},
		{"name":"viewer","sha256":%q,"role":"viewer","projects":["lab"]},
		{"name":"lab-op","sha256":%q,"grants":{"lab":"operator"}},
		{"name":"mixed","sha256":%q,"role":"viewer","projects":["*"],"grants":{"lab":"operator","default":"admin"}},
		{"name":"lab-admin","sha256":%q,"role":"admin","projects":["lab"]}
	]}`, auth.HashToken(tAdmin), auth.HashToken(tViewer), auth.HashToken(tLabOp), auth.HashToken(tMixed), auth.HashToken(tLabAdmin))
	path := filepath.Join(t.TempDir(), "keys.json")
	if err := os.WriteFile(path, []byte(keys), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(auth.Config{Mode: mode, APIKeysFile: path})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus, _ := events.New(200, "", "", "", log)
	au, _ := audit.New(audit.Config{})
	qs, _ := quota.NewStore("", o.limits)
	ss, _ := schedule.NewStore("", nil)
	g, _ := webhooks.NewGuard(nil)
	hooks, _ := webhooks.NewManager(webhooks.Config{Guard: g})
	bus.AddSink(hooks.Dispatch)
	var web fs.FS = fstest.MapFS{"index.html": {Data: []byte("ok")}}
	s := New(Config{
		Provider: demo.New(), Catalog: cat, Events: bus, Auth: a, Metrics: metrics.New(), Web: web,
		Projects: []string{"default", "lab"}, DefaultProject: "default", AuthMode: mode, Log: log,
		Audit: au, Quotas: qs, Schedules: ss, Webhooks: hooks,
		MetricsToken: o.metricsToken, MetricsPublic: o.metricsPublic, DefaultTTLMinutes: o.defaultTTL, RateLimitRPS: o.rps,
	})
	return opsEnv{h: s.Handler(), srv: s, audit: au, hooks: hooks}
}

func (e opsEnv) do(method, path, token string, body any) *httptest.ResponseRecorder {
	var rd io.Reader
	if body != nil {
		switch b := body.(type) {
		case string:
			rd = strings.NewReader(b)
		default:
			j, _ := json.Marshal(b)
			rd = bytes.NewReader(j)
		}
	}
	r := httptest.NewRequest(method, path, rd)
	if token != "" {
		r.Header.Set("Authorization", "Bearer "+token)
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func mustCode(t *testing.T, w *httptest.ResponseRecorder, want int) {
	t.Helper()
	if w.Code != want {
		t.Fatalf("status=%d want %d body=%s", w.Code, want, w.Body.String())
	}
}

func decode[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	return v
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	return decode[struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}](t, w).Error.Code
}

func machineBody(project, name string, cpu int) map[string]any {
	return map[string]any{"project": project, "name": name, "image": "windows-server-2025",
		"compute": map[string]any{"cpu": cpu, "memoryMiB": 8192}, "disk": map[string]any{"sizeGiB": 80}}
}

func createMachineID(t *testing.T, e opsEnv, token, project, name string, cpu int) string {
	t.Helper()
	w := e.do("POST", "/api/v1/machines", token, machineBody(project, name, cpu))
	mustCode(t, w, 201)
	return decode[struct {
		ID string `json:"id"`
	}](t, w).ID
}

// ---- roles ----------------------------------------------------------------

func TestRolesAndPerProjectGrants(t *testing.T) {
	e := opsServer(t, opsOpts{})
	// Legacy key (no role in keys.json) behaves as admin.
	mustCode(t, e.do("POST", "/api/v1/machines", tAdmin, machineBody("default", "legacy-vm", 4)), 201)
	// Viewer reads but cannot write.
	mustCode(t, e.do("GET", "/api/v1/machines?project=lab", tViewer, nil), 200)
	mustCode(t, e.do("POST", "/api/v1/machines", tViewer, machineBody("lab", "v1", 4)), 403)
	// Grants-only key: operator on lab, nothing on default.
	mustCode(t, e.do("POST", "/api/v1/machines", tLabOp, machineBody("lab", "op1", 4)), 201)
	mustCode(t, e.do("POST", "/api/v1/machines", tLabOp, machineBody("default", "op2", 4)), 403)
	mustCode(t, e.do("GET", "/api/v1/machines?project=default", tLabOp, nil), 403)
	// Mixed: viewer on default via "*"... but admin there through grants.
	w := e.do("GET", "/api/v1/projects", tLabOp, nil)
	if got := decode[struct{ Items []string }](t, w).Items; len(got) != 1 || got[0] != "lab" {
		t.Fatalf("grants-only projects: %v", got)
	}
	me := decode[auth.Principal](t, e.do("GET", "/api/v1/me", tMixed, nil))
	if me.Grants["lab"] != auth.Operator || me.Role != auth.Viewer {
		t.Fatalf("me: %+v", me)
	}
	// Admin-only endpoints respect per-project roles.
	mustCode(t, e.do("PUT", "/api/v1/quotas/default", tMixed, quota.Limits{MaxMachines: 5}), 200)
	mustCode(t, e.do("PUT", "/api/v1/quotas/lab", tMixed, quota.Limits{MaxMachines: 5}), 403)
}

// ---- audit ----------------------------------------------------------------

func TestAuditRecordsMutatingCallsOnly(t *testing.T) {
	e := opsServer(t, opsOpts{})
	id := createMachineID(t, e, tLabOp, "lab", "aud-vm", 4)
	mustCode(t, e.do("GET", "/api/v1/machines?project=lab", tLabOp, nil), 200) // not audited
	mustCode(t, e.do("POST", "/api/v1/machines/"+id+"/stop?project=lab", tLabOp, nil), 200)
	mustCode(t, e.do("POST", "/api/v1/machines/"+id+"/start?project=default", tLabOp, nil), 403) // wrong project
	mustCode(t, e.do("POST", "/api/v1/machines", "bad-token", machineBody("lab", "x", 4)), 401)
	mustCode(t, e.do("POST", "/api/v1/machines/"+id+"/snapshot?project=lab", tLabOp, `{"name":"s"}`), 201)

	w := e.do("GET", "/api/v1/audit", tAdmin, nil)
	mustCode(t, w, 200)
	items := decode[struct{ Items []audit.Entry }](t, w).Items
	if len(items) != 5 {
		t.Fatalf("expected 5 audited calls, got %d: %+v", len(items), items)
	}
	// newest first
	snap, anon, denied, stop, create := items[0], items[1], items[2], items[3], items[4]
	if snap.Action != "snapshot.create" || snap.Actor != "lab-op" || snap.Project != "lab" || snap.MachineID != id || snap.Result != "success" || snap.Status != 201 || snap.RequestID == "" {
		t.Fatalf("snapshot entry: %+v", snap)
	}
	if denied.Action != "machine.start" || denied.Result != "denied" || denied.Status != 403 || denied.Project != "default" {
		t.Fatalf("denied entry: %+v", denied)
	}
	if anon.Actor != "anonymous" || anon.Status != 401 || anon.Result != "denied" || anon.Action != "machine.create" {
		t.Fatalf("anon entry: %+v", anon)
	}
	if stop.Action != "machine.stop" || create.Action != "machine.create" || create.MachineID != id || create.Role != "per-project" {
		t.Fatalf("entries: %+v / %+v", stop, create)
	}

	// Filters and pagination.
	f := decode[struct {
		Items      []audit.Entry
		NextCursor string
	}](t, e.do("GET", "/api/v1/audit?actor=lab-op&limit=2", tAdmin, nil))
	if len(f.Items) != 2 || f.NextCursor == "" {
		t.Fatalf("page 1: %+v", f)
	}
	f2 := decode[struct{ Items []audit.Entry }](t, e.do("GET", "/api/v1/audit?actor=lab-op&limit=2&cursor="+f.NextCursor, tAdmin, nil))
	if len(f2.Items) != 2 {
		t.Fatalf("page 2: %+v", f2)
	}
	for _, q := range []string{"result=denied", "action=machine.*", "machineId=" + id, "project=lab", "since=2000-01-01T00:00:00Z", "until=2999-01-01T00:00:00Z"} {
		mustCode(t, e.do("GET", "/api/v1/audit?"+q, tAdmin, nil), 200)
	}
	if n := len(decode[struct{ Items []audit.Entry }](t, e.do("GET", "/api/v1/audit?result=denied", tAdmin, nil)).Items); n != 2 {
		t.Fatalf("denied filter: %d", n)
	}
	mustCode(t, e.do("GET", "/api/v1/audit?since=yesterday", tAdmin, nil), 400)
	mustCode(t, e.do("GET", "/api/v1/audit?result=weird", tAdmin, nil), 400)
	mustCode(t, e.do("GET", "/api/v1/audit?cursor=!!", tAdmin, nil), 400)
}

func TestAuditAccessControl(t *testing.T) {
	e := opsServer(t, opsOpts{})
	createMachineID(t, e, tAdmin, "default", "d-vm", 4)
	createMachineID(t, e, tAdmin, "lab", "l-vm", 4)
	mustCode(t, e.do("GET", "/api/v1/audit", tViewer, nil), 403)
	mustCode(t, e.do("GET", "/api/v1/audit", tLabOp, nil), 403)
	// Project admin only sees (and may only filter) their own project.
	items := decode[struct{ Items []audit.Entry }](t, e.do("GET", "/api/v1/audit", tLabAdmin, nil)).Items
	if len(items) != 1 || items[0].Project != "lab" {
		t.Fatalf("lab admin sees: %+v", items)
	}
	mustCode(t, e.do("GET", "/api/v1/audit?project=default", tLabAdmin, nil), 403)
	// The log is append-only: no mutating verbs are routed.
	mustCode(t, e.do("DELETE", "/api/v1/audit", tAdmin, nil), 405)
}

func TestAuditDisabled(t *testing.T) {
	// A server without an audit store answers 501 rather than panicking.
	h, _ := testServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/audit", nil))
	mustCode(t, w, 501)
}

// ---- metrics --------------------------------------------------------------

func TestMetricsAuthAndContent(t *testing.T) {
	e := opsServer(t, opsOpts{metricsToken: "scrape-me"})
	createMachineID(t, e, tAdmin, "default", "m-default", 4)
	createMachineID(t, e, tAdmin, "lab", "m-lab", 2)
	e.do("GET", "/api/v1/machines?project=lab", tAdmin, nil)
	e.do("GET", "/api/v1/machines", "nope", nil) // 401

	mustCode(t, e.do("GET", "/metrics", "", nil), 401)
	mustCode(t, e.do("GET", "/metrics", "wrong", nil), 401)

	w := e.do("GET", "/metrics", "scrape-me", nil)
	mustCode(t, w, 200)
	out := w.Body.String()
	for _, want := range []string{
		`kryton_machines{project="default",provider="demo",state="running"} 1`,
		`kryton_machines{project="lab",provider="demo",state="running"} 1`,
		`kryton_machines{project="lab",provider="demo",state="stopped"} 0`,
		`kryton_provider_up{provider="demo"} 1`,
		`kryton_api_requests_total{method="POST",route="/api/v1/machines",code="201"} 2`,
		`kryton_api_requests_total{method="GET",route="/api/v1/machines",code="401"} 1`,
		`kryton_http_request_duration_seconds_bucket{method="POST",route="/api/v1/machines",le="+Inf"} 2`,
		`kryton_auth_failures_total`,
		`kryton_quota_usage{project="lab",resource="vcpu"} 2`,
		`kryton_rate_limit_rejections_total 0`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q\n%s", want, out)
		}
	}
	if strings.Contains(out, `/api/v1/machines/`+"\n") {
		t.Error("raw machine ids must not appear in route labels")
	}

	// A project-scoped API key is also accepted but only sees its own projects.
	w = e.do("GET", "/metrics", tViewer, nil)
	mustCode(t, w, 200)
	if strings.Contains(w.Body.String(), `project="default"`) || !strings.Contains(w.Body.String(), `project="lab"`) {
		t.Fatalf("viewer metrics not scoped:\n%s", w.Body.String())
	}
}

func TestMetricsOpenModes(t *testing.T) {
	mustCode(t, opsServer(t, opsOpts{disabledAuth: true}).do("GET", "/metrics", "", nil), 200)
	mustCode(t, opsServer(t, opsOpts{metricsPublic: true}).do("GET", "/metrics", "", nil), 200)
	mustCode(t, opsServer(t, opsOpts{}).do("GET", "/metrics", "", nil), 401)
}

func TestMetricsRateLimitRejections(t *testing.T) {
	e := opsServer(t, opsOpts{rps: 1, metricsToken: "m"})
	limited := 0
	for i := 0; i < 5; i++ {
		if e.do("GET", "/api/v1/me", tAdmin, nil).Code == 429 {
			limited++
		}
	}
	if limited == 0 {
		t.Fatal("expected some 429s")
	}
	out := e.do("GET", "/metrics", "m", nil).Body.String()
	if !strings.Contains(out, fmt.Sprintf("kryton_rate_limit_rejections_total %d", limited)) {
		t.Fatalf("rate limit counter mismatch (limited=%d):\n%s", limited, out)
	}
	if !strings.Contains(out, `code="429"`) {
		t.Fatal("429 not counted in request totals")
	}
}

func TestRouteLabel(t *testing.T) {
	cases := map[string]string{
		"/api/v1/machines":                          "/api/v1/machines",
		"/api/v1/machines/abc-123":                  "/api/v1/machines/{id}",
		"/api/v1/machines/abc/snapshots/s1/restore": "/api/v1/machines/{id}/snapshots/{sid}/restore",
		"/api/v1/machines/abc/console/foo/bar.js":   "/api/v1/machines/{id}/console/*",
		"/api/v1/webhooks/deliveries":               "/api/v1/webhooks/deliveries",
		"/api/v1/webhooks/deliveries/d1/redeliver":  "/api/v1/webhooks/deliveries/{id}/redeliver",
		"/api/v1/webhooks/w1":                       "/api/v1/webhooks/{id}",
		"/api/v1/quotas/lab":                        "/api/v1/quotas/{project}",
		"/api/v1/nope/at/all":                       "/api/unmatched",
		"/assets/app.js":                            "static",
		"/metrics":                                  "/metrics",
	}
	for in, want := range cases {
		if got := routeLabel(in); got != want {
			t.Errorf("%s: got %s want %s", in, got, want)
		}
	}
	if got := auditActionFor("POST", "/api/v1/machines/xyz/start"); got != "machine.start" {
		t.Errorf("action: %s", got)
	}
}

// ---- quotas ---------------------------------------------------------------

func TestQuotaEnforcement(t *testing.T) {
	e := opsServer(t, opsOpts{limits: quota.Limits{MaxMachines: 3, MaxVCPU: 10}})
	createMachineID(t, e, tAdmin, "default", "q1", 4)
	createMachineID(t, e, tAdmin, "default", "q2", 4)
	// 12 vCPU > 10, but 4 alone fits: conflict with current usage.
	w := e.do("POST", "/api/v1/machines", tAdmin, machineBody("default", "q3", 4))
	mustCode(t, w, 409)
	if errCode(t, w) != "QUOTA_EXCEEDED" || !strings.Contains(w.Body.String(), "vCPU limit 10, in use 8, requested 4") {
		t.Fatalf("body=%s", w.Body.String())
	}
	// A single machine bigger than the limit can never fit: forbidden.
	w = e.do("POST", "/api/v1/machines", tAdmin, machineBody("default", "huge", 32))
	mustCode(t, w, 403)
	if errCode(t, w) != "QUOTA_EXCEEDED" {
		t.Fatalf("body=%s", w.Body.String())
	}
	// Machine-count limit.
	createMachineID(t, e, tAdmin, "default", "q4", 2)
	w = e.do("POST", "/api/v1/machines", tAdmin, machineBody("default", "q5", 2))
	mustCode(t, w, 409)
	if !strings.Contains(w.Body.String(), "machines limit 3") {
		t.Fatalf("body=%s", w.Body.String())
	}
	// Quotas are per project: lab uses the same defaults independently.
	createMachineID(t, e, tAdmin, "lab", "lab1", 4)

	// Visibility + admin-managed overrides.
	view := decode[struct {
		Items []quotaView
	}](t, e.do("GET", "/api/v1/quotas", tAdmin, nil))
	if len(view.Items) != 2 || view.Items[0].Usage.Machines != 3 || view.Items[0].Usage.VCPU != 10 || view.Items[0].Explicit {
		t.Fatalf("quota list: %+v", view.Items)
	}
	mustCode(t, e.do("PUT", "/api/v1/quotas/default", tLabAdmin, quota.Limits{MaxMachines: 99}), 403)
	mustCode(t, e.do("PUT", "/api/v1/quotas/default", tAdmin, quota.Limits{MaxMachines: -1}), 400)
	mustCode(t, e.do("PUT", "/api/v1/quotas/nope", tAdmin, quota.Limits{}), 400)
	one := decode[quotaView](t, e.do("PUT", "/api/v1/quotas/default", tAdmin, quota.Limits{MaxMachines: 10, MaxVCPU: 100}))
	if !one.Explicit || one.Limits.MaxMachines != 10 {
		t.Fatalf("put: %+v", one)
	}
	mustCode(t, e.do("POST", "/api/v1/machines", tAdmin, machineBody("default", "q6", 4)), 201)
	mustCode(t, e.do("DELETE", "/api/v1/quotas/default", tAdmin, nil), 204)
	if g := decode[quotaView](t, e.do("GET", "/api/v1/quotas/default", tAdmin, nil)); g.Explicit || g.Limits.MaxMachines != 3 {
		t.Fatalf("fallback to default: %+v", g)
	}
	mustCode(t, e.do("GET", "/api/v1/quotas/default", tViewer, nil), 403)

	out := e.do("GET", "/metrics", tAdmin, nil).Body.String()
	if !strings.Contains(out, `kryton_quota_rejections_total{project="default",resource="vCPU"} 2`) {
		t.Fatalf("rejection metric missing:\n%s", out)
	}
	// Rejections are audited with the reason.
	ents, _, _ := e.audit.Query(audit.Filter{Action: "machine.create", Result: audit.ResultDenied})
	if len(ents) != 1 || !strings.Contains(ents[0].Detail, "quota exceeded") {
		t.Fatalf("audit for quota denial: %+v", ents)
	}
}

func TestQuotaDisabledByDefault(t *testing.T) {
	e := opsServer(t, opsOpts{})
	for i := 0; i < 4; i++ {
		createMachineID(t, e, tAdmin, "default", fmt.Sprintf("free-%d", i), 8)
	}
}

// ---- TTL ------------------------------------------------------------------

func TestMachineTTLOverrideAndDefault(t *testing.T) {
	e := opsServer(t, opsOpts{defaultTTL: 60})
	id := createMachineID(t, e, tAdmin, "default", "ttl-vm", 4)
	get := func() map[string]any {
		return decode[map[string]any](t, e.do("GET", "/api/v1/machines/"+id, tAdmin, nil))
	}
	if get()["expiresAt"] == nil {
		t.Fatal("default TTL should apply when ttlMinutes is omitted")
	}
	w := e.do("PUT", "/api/v1/machines/"+id+"/ttl", tAdmin, `{"ttlMinutes":1440}`)
	mustCode(t, w, 200)
	if get()["expiresAt"] == nil || decode[map[string]any](t, w)["ttlMinutes"].(float64) != 1440 {
		t.Fatal("extend failed")
	}
	mustCode(t, e.do("PUT", "/api/v1/machines/"+id+"/ttl", tAdmin, `{"ttlMinutes":0}`), 200)
	if get()["expiresAt"] != nil {
		t.Fatal("ttlMinutes=0 should clear the expiry")
	}
	list := decode[struct{ Items []map[string]any }](t, e.do("GET", "/api/v1/machines", tAdmin, nil))
	if list.Items[0]["expiresAt"] != nil {
		t.Fatal("list should reflect override")
	}
	mustCode(t, e.do("PUT", "/api/v1/machines/"+id+"/ttl", tAdmin, `{"ttlMinutes":-5}`), 400)
	mustCode(t, e.do("PUT", "/api/v1/machines/"+id+"/ttl", tAdmin, `{"ttlMinutes":999999}`), 400)
	mustCode(t, e.do("PUT", "/api/v1/machines/nope/ttl", tAdmin, `{"ttlMinutes":5}`), 404)
	mustCode(t, e.do("PUT", "/api/v1/machines/"+id+"/ttl", tViewer, `{"ttlMinutes":5}`), 403) // viewer's project is lab; default is not theirs
	// deleting the machine drops the override
	mustCode(t, e.do("DELETE", "/api/v1/machines/"+id, tAdmin, nil), 204)
	if e.srv.schedules.ExpiryCount() != 0 {
		t.Fatal("override should be forgotten when the machine is deleted")
	}
}

// ---- schedules ------------------------------------------------------------

func TestScheduleAPI(t *testing.T) {
	e := opsServer(t, opsOpts{})
	id := createMachineID(t, e, tLabOp, "lab", "sched-vm", 4)
	body := map[string]any{"project": "lab", "machineId": id, "action": "stop", "cron": "0 19 * * mon-fri", "timezone": "Europe/Berlin"}
	w := e.do("POST", "/api/v1/schedules", tLabOp, body)
	mustCode(t, w, 201)
	sc := decode[schedule.Schedule](t, w)
	if sc.NextRunAt == nil || !sc.Enabled || sc.CreatedBy != "lab-op" {
		t.Fatalf("created: %+v", sc)
	}
	bad := func(m map[string]any, want int) {
		t.Helper()
		b := map[string]any{"project": "lab", "machineId": id, "action": "stop", "cron": "0 19 * * *"}
		for k, v := range m {
			b[k] = v
		}
		mustCode(t, e.do("POST", "/api/v1/schedules", tLabOp, b), want)
	}
	bad(map[string]any{"cron": "every day"}, 400)
	bad(map[string]any{"action": "reboot"}, 400)
	bad(map[string]any{"timezone": "Mars/Base"}, 400)
	bad(map[string]any{"machineId": "ghost"}, 404)
	bad(map[string]any{"project": "default"}, 403)
	mustCode(t, e.do("POST", "/api/v1/schedules", tViewer, body), 403)

	list := decode[struct{ Items []schedule.Schedule }](t, e.do("GET", "/api/v1/schedules?project=lab&machineId="+id, tViewer, nil))
	if len(list.Items) != 1 {
		t.Fatalf("list: %+v", list)
	}
	mustCode(t, e.do("GET", "/api/v1/schedules/"+sc.ID, tViewer, nil), 200)
	mustCode(t, e.do("GET", "/api/v1/schedules/"+sc.ID, tMixed, nil), 200)
	// Existence is hidden from callers without access to the project.
	w = e.do("PUT", "/api/v1/schedules/"+sc.ID, tLabOp, `{"enabled":false,"cron":"30 6 * * *"}`)
	mustCode(t, w, 200)
	if up := decode[schedule.Schedule](t, w); up.Enabled || up.Cron != "30 6 * * *" {
		t.Fatalf("update: %+v", up)
	}
	mustCode(t, e.do("PUT", "/api/v1/schedules/"+sc.ID, tLabOp, `{"cron":"junk"}`), 400)
	mustCode(t, e.do("PUT", "/api/v1/schedules/"+sc.ID, tViewer, `{"enabled":true}`), 404)
	mustCode(t, e.do("DELETE", "/api/v1/schedules/"+sc.ID, tLabOp, nil), 204)
	mustCode(t, e.do("DELETE", "/api/v1/schedules/"+sc.ID, tLabOp, nil), 404)

	// Deleting a machine removes its schedules.
	mustCode(t, e.do("POST", "/api/v1/schedules", tLabOp, body), 201)
	mustCode(t, e.do("DELETE", "/api/v1/machines/"+id+"?project=lab", tLabOp, nil), 204)
	if n := len(e.srv.schedules.List("lab", "", nil)); n != 0 {
		t.Fatalf("schedules survived machine delete: %d", n)
	}
}

// ---- webhooks -------------------------------------------------------------

func TestWebhookAPI(t *testing.T) {
	e := opsServer(t, opsOpts{})
	body := map[string]any{"project": "lab", "url": "https://93.184.216.34/hook", "eventTypes": []string{"io.kryton.machine.*"}, "description": "ci"}
	// Only project admins manage webhooks.
	mustCode(t, e.do("POST", "/api/v1/webhooks", tLabOp, body), 403)
	mustCode(t, e.do("POST", "/api/v1/webhooks", tViewer, body), 403)
	w := e.do("POST", "/api/v1/webhooks", tLabAdmin, body)
	mustCode(t, w, 201)
	sub := decode[webhooks.Subscription](t, w)
	if !strings.HasPrefix(sub.Secret, "whsec_") || sub.ID == "" || !sub.Enabled || sub.CreatedBy != "lab-admin" {
		t.Fatalf("created: %+v", sub)
	}
	// SSRF guard and validation errors.
	for url, wantCode := range map[string]string{
		"http://93.184.216.34/hook":       "WEBHOOK_URL_BLOCKED",
		"https://127.0.0.1/hook":          "WEBHOOK_URL_BLOCKED",
		"https://10.1.2.3/hook":           "WEBHOOK_URL_BLOCKED",
		"https://169.254.169.254/latest":  "WEBHOOK_URL_BLOCKED",
		"https://localhost/hook":          "WEBHOOK_URL_BLOCKED",
		"https://user:pw@93.184.216.34/h": "WEBHOOK_URL_BLOCKED",
	} {
		w := e.do("POST", "/api/v1/webhooks", tLabAdmin, map[string]any{"project": "lab", "url": url})
		mustCode(t, w, 400)
		if errCode(t, w) != wantCode {
			t.Errorf("%s: code %s", url, errCode(t, w))
		}
	}
	mustCode(t, e.do("POST", "/api/v1/webhooks", tLabAdmin, map[string]any{"project": "lab", "url": "https://93.184.216.34/x", "eventTypes": []string{"["}}), 400)
	mustCode(t, e.do("POST", "/api/v1/webhooks", tLabAdmin, map[string]any{"project": "ghost", "url": "https://93.184.216.34/x"}), 400)

	// Reads never expose the secret.
	got := e.do("GET", "/api/v1/webhooks/"+sub.ID, tLabOp, nil)
	mustCode(t, got, 200)
	if strings.Contains(got.Body.String(), "whsec_") || strings.Contains(got.Body.String(), `"secret"`) {
		t.Fatalf("secret leaked: %s", got.Body.String())
	}
	list := decode[struct{ Items []webhooks.Subscription }](t, e.do("GET", "/api/v1/webhooks?project=lab", tLabOp, nil))
	if len(list.Items) != 1 || list.Items[0].Secret != "" {
		t.Fatalf("list: %+v", list)
	}
	mustCode(t, e.do("GET", "/api/v1/webhooks?project=lab", tViewer, nil), 403)
	// Another project's admin cannot see it (404, not 403).
	mustCode(t, e.do("GET", "/api/v1/webhooks/"+sub.ID, tViewer, nil), 404)
	mustCode(t, e.do("DELETE", "/api/v1/webhooks/"+sub.ID, tViewer, nil), 404)

	// Events for the project are queued as deliveries; other projects' are not.
	createMachineID(t, e, tAdmin, "lab", "wh-vm", 4)
	createMachineID(t, e, tAdmin, "default", "wh-other", 4)
	dl := decode[struct{ Items []webhooks.Delivery }](t, e.do("GET", "/api/v1/webhooks/deliveries?project=lab", tLabAdmin, nil))
	if len(dl.Items) != 1 || dl.Items[0].EventType != "io.kryton.machine.created" || dl.Items[0].Status != "pending" || len(dl.Items[0].Payload) != 0 {
		t.Fatalf("deliveries: %+v", dl.Items)
	}
	one := decode[webhooks.Delivery](t, e.do("GET", "/api/v1/webhooks/deliveries/"+dl.Items[0].ID, tLabAdmin, nil))
	if len(one.Payload) == 0 {
		t.Fatal("delivery detail should include the payload")
	}
	mustCode(t, e.do("GET", "/api/v1/webhooks/deliveries/"+dl.Items[0].ID, tViewer, nil), 404)
	mustCode(t, e.do("GET", "/api/v1/webhooks/deliveries?project=lab&status=bogus", tLabAdmin, nil), 400)
	dead := decode[struct{ Items []webhooks.Delivery }](t, e.do("GET", "/api/v1/webhooks/dead-letters?project=lab", tLabAdmin, nil))
	if len(dead.Items) != 0 {
		t.Fatalf("dead letters: %+v", dead.Items)
	}
	mustCode(t, e.do("POST", "/api/v1/webhooks/"+sub.ID+"/test", tLabAdmin, nil), 202)
	mustCode(t, e.do("POST", "/api/v1/webhooks/deliveries/"+dl.Items[0].ID+"/redeliver", tLabOp, nil), 404)
	mustCode(t, e.do("POST", "/api/v1/webhooks/deliveries/"+dl.Items[0].ID+"/redeliver", tLabAdmin, nil), 202)

	// Update: disable and re-validate URL.
	mustCode(t, e.do("PUT", "/api/v1/webhooks/"+sub.ID, tLabAdmin, `{"enabled":false}`), 200)
	mustCode(t, e.do("PUT", "/api/v1/webhooks/"+sub.ID, tLabAdmin, `{"url":"http://insecure.example/x"}`), 400)
	mustCode(t, e.do("DELETE", "/api/v1/webhooks/"+sub.ID, tLabAdmin, nil), 204)
	mustCode(t, e.do("DELETE", "/api/v1/webhooks/"+sub.ID, tLabAdmin, nil), 404)

	// Subscription lifecycle is audited (secrets are not).
	ents, _, _ := e.audit.Query(audit.Filter{Action: "webhook.*", Limit: 100})
	if len(ents) < 5 {
		t.Fatalf("webhook audit entries: %d", len(ents))
	}
	raw, _ := json.Marshal(ents)
	if strings.Contains(string(raw), "whsec_") {
		t.Fatal("audit log must not contain secrets")
	}
}

func TestOpsEndpointsReturn501WhenNotConfigured(t *testing.T) {
	h, _ := testServer(t)
	for _, c := range []struct{ method, path string }{
		{"GET", "/api/v1/webhooks"}, {"GET", "/api/v1/schedules"}, {"GET", "/api/v1/quotas"},
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(c.method, c.path, nil))
		mustCode(t, w, 501)
	}
}
