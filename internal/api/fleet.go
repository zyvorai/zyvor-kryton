// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

// ---- Idempotency-Key for POST /machines -----------------------------------

const (
	idemTTL        = 24 * time.Hour
	idemMaxEntries = 10000
	idemMaxBody    = 64 << 10
)

var idemKeyPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]{1,128}$`)

type idemState int

const (
	idemNew idemState = iota
	idemReplay
	idemMismatch
	idemInFlight
	idemFull
)

type idemEntry struct {
	fingerprint string
	done        bool
	status      int
	body        []byte
	expires     time.Time
}

// idemStore remembers the first result per (caller, project, key) for 24 h.
// It is in memory only: a restart forgets every key. Lookups hash the
// caller identity into the entry key so one caller cannot replay another's
// result by guessing a key.
type idemStore struct {
	mu  sync.Mutex
	m   map[string]*idemEntry
	now func() time.Time
}

func newIdemStore() *idemStore {
	return &idemStore{m: map[string]*idemEntry{}, now: time.Now}
}

func idemLookupKey(principal, project, key string) string {
	sum := sha256.Sum256([]byte(principal + "\x00" + project + "\x00" + key))
	return hex.EncodeToString(sum[:])
}

func (s *idemStore) begin(lk, fingerprint string) (idemState, *idemEntry) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now()
	if e, ok := s.m[lk]; ok {
		if now.After(e.expires) {
			delete(s.m, lk)
		} else if e.fingerprint != fingerprint {
			return idemMismatch, nil
		} else if !e.done {
			return idemInFlight, nil
		} else {
			cp := *e
			return idemReplay, &cp
		}
	}
	if len(s.m) >= idemMaxEntries {
		for k, e := range s.m {
			if now.After(e.expires) {
				delete(s.m, k)
			}
		}
		if len(s.m) >= idemMaxEntries {
			return idemFull, nil
		}
	}
	s.m[lk] = &idemEntry{fingerprint: fingerprint, expires: now.Add(idemTTL)}
	return idemNew, nil
}

func (s *idemStore) complete(lk string, status int, body []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[lk]; ok {
		e.done, e.status, e.body = true, status, body
	}
}

// abandon forgets an unfinished key so a failed request can be retried.
func (s *idemStore) abandon(lk string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.m[lk]; ok && !e.done {
		delete(s.m, lk)
	}
}

// idempotencyKey extracts and validates the Idempotency-Key header. An
// absent header yields "" with no error. The IETF draft makes the value a
// Structured Field string, so surrounding double quotes are accepted.
func idempotencyKey(r *http.Request) (string, error) {
	vals := r.Header.Values("Idempotency-Key")
	if len(vals) == 0 {
		return "", nil
	}
	if len(vals) > 1 {
		return "", errors.New("Idempotency-Key must be sent once")
	}
	v := strings.TrimSpace(vals[0])
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	if !idemKeyPattern.MatchString(v) {
		return "", errors.New("Idempotency-Key must be 1-128 characters from A-Z a-z 0-9 _ . : -")
	}
	return v, nil
}

func idemFingerprint(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// ---- Selectors ------------------------------------------------------------

// parseSelectorParam reads ?selector= (Kubernetes-style label selector).
func parseSelectorParam(raw string) (model.Selector, error) {
	return model.ParseSelector(raw)
}

func filterBySelector(ms []model.Machine, sel model.Selector) []model.Machine {
	if len(sel) == 0 {
		return ms
	}
	out := ms[:0:0]
	for _, m := range ms {
		if sel.Matches(m.Spec.Labels) {
			out = append(out, m)
		}
	}
	return out
}

// ---- Bulk lifecycle -------------------------------------------------------

const maxBulkMachines = 100

var machineIDPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

type bulkActionRequest struct {
	Project  string   `json:"project,omitempty"`
	Action   string   `json:"action"`
	IDs      []string `json:"ids,omitempty"`
	Selector string   `json:"selector,omitempty"`
	// DryRun validates every target against its current state and reports
	// what would happen without changing anything (EC2 DryRun style).
	DryRun bool `json:"dryRun,omitempty"`
	// Confirm must be true for a real (non dry-run) bulk delete.
	Confirm bool `json:"confirm,omitempty"`
	// SnapshotName names the snapshots of a bulk snapshot.
	SnapshotName string `json:"snapshotName,omitempty"`
}

type bulkResult struct {
	ID    string             `json:"id"`
	Name  string             `json:"name,omitempty"`
	OK    bool               `json:"ok"`
	State model.MachineState `json:"state,omitempty"`
	// SnapshotID is set by a bulk snapshot.
	SnapshotID string    `json:"snapshotId,omitempty"`
	Error      *APIError `json:"error,omitempty"`
}

type bulkActionResponse struct {
	Project   string       `json:"project"`
	Action    string       `json:"action"`
	DryRun    bool         `json:"dryRun,omitempty"`
	Total     int          `json:"total"`
	Succeeded int          `json:"succeeded"`
	Failed    int          `json:"failed"`
	Results   []bulkResult `json:"results"`
}

// bulkMachineAction starts, stops, snapshots or deletes up to 100 machines named by id or by
// label selector. Each machine gets its own result; one failure does not
// stop the rest, and the HTTP status is 200 whenever the request itself was
// valid (inspect `failed`).
func (s *Server) bulkMachineAction(w http.ResponseWriter, r *http.Request) {
	var req bulkActionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	if req.Project == "" {
		req.Project = s.defaultProject
	}
	if !s.projectConfigured(req.Project) {
		s.badRequest(w, r, "project is not configured")
		return
	}
	auditNote(r.Context(), req.Project, "")
	if !auth.Can(auth.FromContext(r.Context()), req.Project, auth.Operator) {
		s.forbidden(w, r)
		return
	}
	switch req.Action {
	case "start", "stop", "snapshot", "delete":
	default:
		s.badRequest(w, r, `action must be "start", "stop", "snapshot" or "delete"`)
		return
	}
	if req.Action == "delete" && !req.DryRun && !req.Confirm {
		s.badRequest(w, r, `bulk delete needs "confirm": true (or "dryRun": true to preview)`)
		return
	}
	if req.SnapshotName != "" && req.Action != "snapshot" {
		s.badRequest(w, r, "snapshotName only applies to the snapshot action")
		return
	}
	if len(req.SnapshotName) > 128 {
		s.badRequest(w, r, "snapshotName is at most 128 characters")
		return
	}
	if (len(req.IDs) == 0) == (strings.TrimSpace(req.Selector) == "") {
		s.badRequest(w, r, "provide exactly one of ids or selector")
		return
	}
	var ids []string
	names := map[string]string{}
	if len(req.IDs) > 0 {
		if len(req.IDs) > maxBulkMachines {
			s.badRequest(w, r, "at most 100 machines per request")
			return
		}
		seen := map[string]bool{}
		for _, id := range req.IDs {
			if !machineIDPattern.MatchString(id) {
				s.badRequest(w, r, "ids must be machine IDs of 1-64 letters, digits or dashes")
				return
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	} else {
		sel, err := parseSelectorParam(req.Selector)
		if err != nil {
			s.badRequest(w, r, err.Error())
			return
		}
		ms, err := s.p.List(r.Context(), req.Project)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		ms = filterBySelector(ms, sel)
		if len(ms) > maxBulkMachines {
			s.badRequest(w, r, "selector matches more than 100 machines; narrow it")
			return
		}
		for _, m := range ms {
			ids = append(ids, m.ID)
			names[m.ID] = m.Spec.Name
		}
	}
	resp := bulkActionResponse{Project: req.Project, Action: req.Action, DryRun: req.DryRun, Total: len(ids), Results: make([]bulkResult, 0, len(ids))}
	ac := s.actionContext(r.Context())
	for _, id := range ids {
		if r.Context().Err() != nil {
			break
		}
		res := bulkResult{ID: id, Name: names[id]}
		err := s.bulkOne(r, req, ac, &res)
		if err != nil {
			c := classifyError(err)
			res.Error = &APIError{Code: c.Code, Message: c.Message}
			resp.Failed++
		} else {
			res.OK = true
			resp.Succeeded++
		}
		resp.Results = append(resp.Results, res)
	}
	s.metrics.Operation("bulk-" + req.Action)
	jsonResponse(w, http.StatusOK, resp)
}

// ---- Event filters --------------------------------------------------------

type eventFilter struct {
	types     []string
	machineID string
	since     time.Time
}

func parseEventFilter(r *http.Request) (eventFilter, error) {
	var f eventFilter
	q := r.URL.Query()
	if t := q.Get("type"); t != "" {
		f.types = strings.Split(t, ",")
		if len(f.types) > 8 {
			return f, errors.New("type accepts at most 8 values")
		}
		for i, v := range f.types {
			v = strings.TrimSpace(v)
			f.types[i] = v
			if len(v) == 0 || len(v) > 128 || !labelishPattern.MatchString(v) {
				return f, errors.New("type values must be event types, optionally ending in *")
			}
		}
	}
	if id := q.Get("machineId"); id != "" {
		if !machineIDPattern.MatchString(id) {
			return f, errors.New("machineId is invalid")
		}
		f.machineID = id
	}
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339, s)
		if err != nil {
			return f, errors.New("since must be an RFC 3339 timestamp")
		}
		f.since = t
	}
	return f, nil
}

var labelishPattern = regexp.MustCompile(`^[A-Za-z0-9_.:-]+\*?$`)

func (f eventFilter) match(e events.Event) bool {
	if len(f.types) > 0 {
		ok := false
		for _, t := range f.types {
			if p, wild := strings.CutSuffix(t, "*"); wild && strings.HasPrefix(e.Type, p) || e.Type == t {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	if f.machineID != "" {
		id, _ := e.Data["machineId"].(string)
		if id != f.machineID && e.Subject != "machines/"+f.machineID {
			return false
		}
	}
	if !f.since.IsZero() && e.Time.Before(f.since) {
		return false
	}
	return true
}

// bulkOne runs (or, for a dry run, validates) one action on one machine and
// fills res on success.
func (s *Server) bulkOne(r *http.Request, req bulkActionRequest, ac actionContext, res *bulkResult) error {
	if req.DryRun {
		m, err := s.p.Get(r.Context(), req.Project, res.ID)
		if err != nil {
			return err
		}
		res.Name, res.State = m.Spec.Name, m.State
		if req.Action == "delete" && s.protections != nil {
			release, perr := s.protections.BeginDelete(req.Project, res.ID)
			if perr != nil {
				return perr
			}
			release()
		}
		if !hasAction(ac.actionsFor(m.State), req.Action) {
			return fmt.Errorf("%w: cannot %s a machine that is %s", provider.ErrPrecondition, req.Action, m.State)
		}
		return nil
	}
	switch req.Action {
	case "delete":
		name, err := s.deleteOne(r, req.Project, res.ID)
		if name != "" {
			res.Name = name
		}
		if err != nil {
			return err
		}
		res.State = model.StateDeleting
		return nil
	case "snapshot":
		snap, err := s.snapshotOne(r, req.Project, res.ID, req.SnapshotName)
		if err != nil {
			return err
		}
		res.SnapshotID = snap.ID
		if m, gerr := s.p.Get(r.Context(), req.Project, res.ID); gerr == nil {
			res.Name, res.State = m.Spec.Name, m.State
		}
		return nil
	default:
		m, err := s.lifecycle(r, req.Project, res.ID, req.Action)
		if err != nil {
			return err
		}
		res.Name, res.State = m.Spec.Name, m.State
		return nil
	}
}
