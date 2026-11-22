// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zyvorai/kryton/internal/audit"
	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/metrics"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/quota"
)

// ---- route templates ------------------------------------------------------

// routeTemplates lists every API path template; routeLabel maps a concrete
// request path onto one so metrics and audit actions stay low-cardinality.
var routeTemplates = [][]string{}

func init() {
	for _, t := range []string{
		"/api/v1", "/api/v1/me", "/api/v1/projects", "/api/v1/capabilities", "/api/v1/doctor",
		"/api/v1/storage", "/api/v1/storage/config", "/api/v1/storage/setup",
		"/api/v1/settings", "/api/v1/settings/test", "/api/v1/integrations/atlas/test",
		"/api/v1/images", "/api/v1/golden", "/api/v1/golden/{id}", "/api/v1/golden/{id}/passport", "/api/v1/golden/{id}/bootstrap",
		"/api/v1/jobs", "/api/v1/jobs/{id}", "/api/v1/summary", "/api/v1/lab/bootstrap",
		"/api/v1/machines", "/api/v1/machines/{id}", "/api/v1/machines/{id}/start", "/api/v1/machines/{id}/stop",
		"/api/v1/machines/{id}/snapshot", "/api/v1/machines/{id}/snapshots", "/api/v1/machines/{id}/snapshots/{sid}",
		"/api/v1/machines/{id}/snapshots/{sid}/restore", "/api/v1/machines/{id}/console-ticket",
		"/api/v1/machines/{id}/vnc", "/api/v1/machines/{id}/console", "/api/v1/machines/{id}/ttl",
		"/api/v1/events", "/api/v1/events/stream", "/api/v1/audit",
		"/api/v1/webhooks", "/api/v1/webhooks/deliveries", "/api/v1/webhooks/dead-letters",
		"/api/v1/webhooks/deliveries/{id}", "/api/v1/webhooks/deliveries/{id}/redeliver",
		"/api/v1/webhooks/{id}", "/api/v1/webhooks/{id}/test",
		"/api/v1/schedules", "/api/v1/schedules/{id}",
		"/api/v1/quotas", "/api/v1/quotas/{project}",
	} {
		routeTemplates = append(routeTemplates, strings.Split(strings.Trim(t, "/"), "/"))
	}
}

// routeLabel maps a request path to its route template.
func routeLabel(path string) string {
	switch path {
	case "/healthz", "/readyz", "/metrics", "/openapi.yaml", "/api/openapi.yaml":
		return path
	}
	if !strings.HasPrefix(path, "/api/") {
		return "static"
	}
	segs := strings.Split(strings.Trim(path, "/"), "/")
	if len(segs) > 5 && segs[2] == "machines" && segs[4] == "console" {
		return "/api/v1/machines/{id}/console/*"
	}
	best, bestLit := "", -1
	for _, t := range routeTemplates {
		if len(t) != len(segs) {
			continue
		}
		lit, ok := 0, true
		for i := range t {
			if strings.HasPrefix(t[i], "{") {
				continue
			}
			if t[i] != segs[i] {
				ok = false
				break
			}
			lit++
		}
		if ok && lit > bestLit {
			best, bestLit = "/"+strings.Join(t, "/"), lit
		}
	}
	if best == "" {
		return "/api/unmatched"
	}
	return best
}

var auditActions = map[string]string{
	"POST /api/v1/machines":                              "machine.create",
	"DELETE /api/v1/machines/{id}":                       "machine.delete",
	"POST /api/v1/machines/{id}/start":                   "machine.start",
	"POST /api/v1/machines/{id}/stop":                    "machine.stop",
	"PUT /api/v1/machines/{id}/ttl":                      "machine.ttl.update",
	"POST /api/v1/machines/{id}/snapshot":                "snapshot.create",
	"POST /api/v1/machines/{id}/snapshots/{sid}/restore": "snapshot.restore",
	"DELETE /api/v1/machines/{id}/snapshots/{sid}":       "snapshot.delete",
	"POST /api/v1/machines/{id}/console-ticket":          "console.ticket",
	"PUT /api/v1/settings":                               "settings.update",
	"POST /api/v1/settings/test":                         "settings.test",
	"PUT /api/v1/storage/config":                         "storage.config.update",
	"POST /api/v1/storage/setup":                         "storage.setup",
	"POST /api/v1/integrations/atlas/test":               "atlas.test",
	"POST /api/v1/golden":                                "golden.start",
	"POST /api/v1/golden/{id}/bootstrap":                 "golden.bootstrap",
	"POST /api/v1/webhooks":                              "webhook.create",
	"PUT /api/v1/webhooks/{id}":                          "webhook.update",
	"DELETE /api/v1/webhooks/{id}":                       "webhook.delete",
	"POST /api/v1/webhooks/{id}/test":                    "webhook.test",
	"POST /api/v1/webhooks/deliveries/{id}/redeliver":    "webhook.redeliver",
	"POST /api/v1/schedules":                             "schedule.create",
	"PUT /api/v1/schedules/{id}":                         "schedule.update",
	"DELETE /api/v1/schedules/{id}":                      "schedule.delete",
	"PUT /api/v1/quotas/{project}":                       "quota.update",
	"DELETE /api/v1/quotas/{project}":                    "quota.delete",
}

func auditActionFor(method, path string) string {
	route := routeLabel(path)
	if a, ok := auditActions[method+" "+route]; ok {
		return a
	}
	return strings.ToLower(method) + " " + route
}

// ---- audit middleware -----------------------------------------------------

type auditKey struct{}

// auditInfo is filled in by the handlers a request passes through and read
// back by auditMiddleware once the response status is known.
type auditInfo struct {
	mu      sync.Mutex
	actor   string
	role    string
	project string
	machine string
	detail  string
}

func auditFrom(ctx context.Context) *auditInfo { i, _ := ctx.Value(auditKey{}).(*auditInfo); return i }

// auditNote records the project and machine a request touched. Empty
// values never overwrite earlier ones. A no-op outside an audited request.
func auditNote(ctx context.Context, project, machine string) {
	if i := auditFrom(ctx); i != nil {
		i.mu.Lock()
		if project != "" {
			i.project = project
		}
		if machine != "" {
			i.machine = machine
		}
		i.mu.Unlock()
	}
}

func auditDetail(ctx context.Context, detail string) {
	if i := auditFrom(ctx); i != nil {
		i.mu.Lock()
		i.detail = detail
		i.mu.Unlock()
	}
}

func isMutating(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// auditMiddleware appends one audit entry per mutating API call, including
// those rejected by auth (401), authorization (403) or rate limiting (429).
func (s *Server) auditMiddleware(next http.Handler) http.Handler {
	if s.audit == nil {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isMutating(r.Method) {
			next.ServeHTTP(w, r)
			return
		}
		info := &auditInfo{actor: "anonymous"}
		sw := &statusWriter{ResponseWriter: w}
		next.ServeHTTP(sw, r.WithContext(context.WithValue(r.Context(), auditKey{}, info)))
		status := sw.status
		if status == 0 {
			status = http.StatusOK
		}
		info.mu.Lock()
		e := audit.Entry{
			Actor: info.actor, Role: info.role, Project: info.project, MachineID: info.machine, Detail: info.detail,
			Action: auditActionFor(r.Method, r.URL.Path), Method: r.Method, Path: r.URL.Path, Status: status,
			RequestID: requestID(r.Context()), Remote: remoteHost(r),
		}
		info.mu.Unlock()
		if _, err := s.audit.Record(e); err != nil {
			s.log.Error("audit sink write failed", "error", err, "request_id", e.RequestID)
		}
	})
}

// auditActor runs inside authentication and tells the audit record who called.
func (s *Server) auditActor(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if i := auditFrom(r.Context()); i != nil {
			p := auth.FromContext(r.Context())
			i.mu.Lock()
			i.actor = p.Name
			i.role = string(p.Role)
			if p.Role == "" && len(p.Grants) > 0 {
				i.role = "per-project"
			}
			i.mu.Unlock()
		}
		next.ServeHTTP(w, r)
	})
}

func remoteHost(r *http.Request) string {
	h := r.RemoteAddr
	if i := strings.LastIndex(h, ":"); i > 0 {
		h = h[:i]
	}
	return strings.Trim(h, "[]")
}

func (s *Server) listAudit(w http.ResponseWriter, r *http.Request) {
	if s.audit == nil {
		s.writeAPIError(w, r, http.StatusNotImplemented, "NOT_CONFIGURED", "audit log is not enabled")
		return
	}
	p := auth.FromContext(r.Context())
	admin := false
	for _, proj := range s.projects {
		if auth.Can(p, proj, auth.Admin) {
			admin = true
			break
		}
	}
	if !admin {
		s.forbidden(w, r)
		return
	}
	q := r.URL.Query()
	f := audit.Filter{
		Project: q.Get("project"), Actor: q.Get("actor"), Action: q.Get("action"),
		MachineID: q.Get("machineId"), Result: q.Get("result"), Cursor: q.Get("cursor"),
		Visible: func(project string) bool {
			if project == "" {
				return auth.IsGlobalAdmin(p)
			}
			return auth.Can(p, project, auth.Admin)
		},
	}
	if f.Project != "" && !auth.Can(p, f.Project, auth.Admin) {
		s.forbidden(w, r)
		return
	}
	if f.Result != "" && f.Result != audit.ResultSuccess && f.Result != audit.ResultDenied && f.Result != audit.ResultError {
		s.badRequest(w, r, "result must be success, denied or error")
		return
	}
	for name, dst := range map[string]*time.Time{"since": &f.Since, "until": &f.Until} {
		if v := q.Get(name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				s.badRequest(w, r, name+" must be an RFC 3339 timestamp")
				return
			}
			*dst = t
		}
	}
	if v := q.Get("limit"); v != "" {
		n := 0
		for _, c := range v {
			if c < '0' || c > '9' || n > 1000 {
				s.badRequest(w, r, "limit must be a positive integer")
				return
			}
			n = n*10 + int(c-'0')
		}
		f.Limit = n
	}
	items, next, err := s.audit.Query(f)
	if err != nil {
		s.badRequest(w, r, "invalid cursor")
		return
	}
	jsonResponse(w, http.StatusOK, listResponse[audit.Entry]{Items: items, NextCursor: next})
}

// ---- metrics --------------------------------------------------------------

type scrapeCache struct {
	mu       sync.Mutex
	at       time.Time
	machines map[string]map[model.MachineState]int
	usage    map[string]quota.Usage
	up       bool
}

const scrapeTTL = 10 * time.Second

// refresh re-reads provider state at most once per scrapeTTL so an
// aggressive scraper cannot hammer the backing provider.
func (s *Server) refreshScrape() scrapeCache {
	c := &s.scrape
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.at.IsZero() && time.Since(c.at) < scrapeTTL {
		return scrapeCache{machines: c.machines, usage: c.usage, up: c.up}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c.up = s.p.Health(ctx) == nil
	c.machines = map[string]map[model.MachineState]int{}
	c.usage = map[string]quota.Usage{}
	for _, project := range s.projects {
		ms, err := s.p.List(ctx, project)
		if err != nil {
			continue
		}
		counts := map[model.MachineState]int{}
		for _, m := range ms {
			counts[m.State]++
		}
		c.machines[project] = counts
		c.usage[project] = quota.UsageOf(ms)
	}
	c.at = time.Now()
	return scrapeCache{machines: c.machines, usage: c.usage, up: c.up}
}

func (s *Server) collectMetrics(visible func(string) bool) []metrics.Family {
	snap := s.refreshScrape()
	show := func(p string) bool { return visible == nil || visible(p) }
	provider := s.p.Name()
	up := 0.0
	if snap.up {
		up = 1
	}
	fams := []metrics.Family{{
		Name: "kryton_provider_up", Help: "1 if the backing provider's health check passes.",
		Samples: []metrics.Sample{{Labels: map[string]string{"provider": provider}, Value: up}},
	}}
	machines := metrics.Family{Name: "kryton_machines", Help: "Machines by project, provider and lifecycle state."}
	projects := make([]string, 0, len(snap.machines))
	for p := range snap.machines {
		projects = append(projects, p)
	}
	sort.Strings(projects)
	states := []model.MachineState{model.StatePending, model.StateProvisioning, model.StateStarting, model.StateRunning, model.StateStopping, model.StateStopped, model.StateMigrating, model.StatePaused, model.StateDeleting, model.StateFailed, model.StateUnknown}
	for _, p := range projects {
		if !show(p) {
			continue
		}
		for _, st := range states {
			machines.Samples = append(machines.Samples, metrics.Sample{
				Labels: map[string]string{"project": p, "provider": provider, "state": string(st)}, Value: float64(snap.machines[p][st])})
		}
	}
	fams = append(fams, machines)
	if s.quotas != nil {
		lim := metrics.Family{Name: "kryton_quota_limit", Help: "Configured per-project quota (absent when unlimited)."}
		use := metrics.Family{Name: "kryton_quota_usage", Help: "Current per-project resource usage, by quota resource."}
		for _, p := range projects {
			if !show(p) {
				continue
			}
			l, u := s.quotas.Get(p), snap.usage[p]
			for _, d := range []struct {
				res        string
				limit, use int
			}{{"machines", l.MaxMachines, u.Machines}, {"vcpu", l.MaxVCPU, u.VCPU}, {"memory_mib", l.MaxMemoryMiB, u.MemoryMiB}, {"disk_gib", l.MaxDiskGiB, u.DiskGiB}} {
				lb := map[string]string{"project": p, "resource": d.res}
				use.Samples = append(use.Samples, metrics.Sample{Labels: lb, Value: float64(d.use)})
				if d.limit > 0 {
					lim.Samples = append(lim.Samples, metrics.Sample{Labels: lb, Value: float64(d.limit)})
				}
			}
		}
		fams = append(fams, lim, use)
	}
	if s.webhooks != nil {
		dead := metrics.Family{Name: "kryton_webhook_dead_letters", Help: "Webhook deliveries in the dead-letter list, by project."}
		for p, n := range s.webhooks.DeadLetterCount() {
			if show(p) {
				dead.Samples = append(dead.Samples, metrics.Sample{Labels: map[string]string{"project": p}, Value: float64(n)})
			}
		}
		fams = append(fams, dead)
	}
	if s.audit != nil && show("") {
		fams = append(fams, metrics.Family{Name: "kryton_audit_entries", Help: "Audit entries currently held in memory.",
			Samples: []metrics.Sample{{Value: float64(s.audit.Len())}}})
	}
	return fams
}

// serveMetrics gates /metrics: open when API auth is disabled or
// KRYTON_METRICS_PUBLIC=true; otherwise the dedicated metrics token (full
// view) or any valid API key/proxy identity (limited to its own projects).
func (s *Server) serveMetrics(w http.ResponseWriter, r *http.Request) {
	if s.authMode == "disabled" || s.metricsPublic {
		s.metrics.Serve(w, nil)
		return
	}
	if s.metricsToken != "" {
		if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
			got := sha256.Sum256([]byte(strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))))
			want := sha256.Sum256([]byte(s.metricsToken))
			if subtle.ConstantTimeCompare(got[:], want[:]) == 1 {
				s.metrics.Serve(w, nil)
				return
			}
		}
	}
	p, err := s.auth.Authenticate(r)
	if err != nil {
		w.Header().Set("WWW-Authenticate", `Bearer realm="kryton-metrics"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	s.metrics.Serve(w, func(project string) bool {
		if project == "" {
			return auth.IsGlobalAdmin(p)
		}
		return auth.Can(p, project, auth.Viewer)
	})
}

// ---- TTL override ---------------------------------------------------------

// applyExpiry overlays an operator TTL override onto a machine response.
func (s *Server) applyExpiry(m *model.Machine) {
	if s.schedules == nil || m == nil {
		return
	}
	if at, ok := s.schedules.Expiry(m.Project, m.ID); ok {
		m.ExpiresAt = at
	}
}

type ttlRequest struct {
	// TTLMinutes sets the machine to expire this many minutes from now;
	// 0 removes the expiry (the machine never auto-deletes).
	TTLMinutes int `json:"ttlMinutes"`
}

func (s *Server) setMachineTTL(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Operator)
	if !ok {
		return
	}
	if s.schedules == nil {
		s.writeAPIError(w, r, http.StatusNotImplemented, "NOT_CONFIGURED", "TTL management is not enabled")
		return
	}
	var req ttlRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	if req.TTLMinutes < 0 || req.TTLMinutes > 43200 {
		s.badRequest(w, r, "ttlMinutes must be between 0 (never expire) and 43200")
		return
	}
	id := r.PathValue("id")
	m, err := s.p.Get(r.Context(), project, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	var at *time.Time
	if req.TTLMinutes > 0 {
		t := time.Now().UTC().Add(time.Duration(req.TTLMinutes) * time.Minute)
		at = &t
	}
	if _, err := s.schedules.SetExpiry(project, id, at, auth.FromContext(r.Context()).Name); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.metrics.Operation("ttl-update")
	s.events.Publish(r.Context(), "io.kryton.machine.ttl.updated", "machines/"+id, map[string]any{"project": project, "machineId": id, "name": m.Spec.Name, "ttlMinutes": req.TTLMinutes, "expiresAt": at})
	jsonResponse(w, http.StatusOK, map[string]any{"project": project, "machineId": id, "ttlMinutes": req.TTLMinutes, "expiresAt": at})
}
