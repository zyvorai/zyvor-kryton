// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

// Package schedule provides cron-style scheduled power actions (start,
// stop, restart, snapshot) for machines, evaluated in an IANA timezone,
// and per-machine expiry overrides (TTL extend / never-expire) that the
// TTL reconciler honours. State is persisted to a JSON file.
package schedule

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/zyvorai/kryton/internal/id"
)

// Actions a schedule may run.
const (
	ActionStart    = "start"
	ActionStop     = "stop"
	ActionRestart  = "restart"
	ActionSnapshot = "snapshot"
)

// MaxPerProject caps schedules per project.
const MaxPerProject = 100

// ErrNotFound is returned for unknown schedule ids.
var ErrNotFound = errors.New("schedule not found")

// ErrLimit is returned when a project has MaxPerProject schedules.
var ErrLimit = errors.New("schedule limit reached for project")

// ValidAction reports whether a is a supported action.
func ValidAction(a string) bool {
	switch a {
	case ActionStart, ActionStop, ActionRestart, ActionSnapshot:
		return true
	}
	return false
}

// Schedule runs Action on MachineID whenever Cron matches in Timezone.
type Schedule struct {
	ID           string     `json:"id"`
	Project      string     `json:"project"`
	MachineID    string     `json:"machineId"`
	Action       string     `json:"action"`
	Cron         string     `json:"cron"`
	Timezone     string     `json:"timezone"`
	SnapshotName string     `json:"snapshotName,omitempty"`
	Enabled      bool       `json:"enabled"`
	CreatedBy    string     `json:"createdBy,omitempty"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	NextRunAt    *time.Time `json:"nextRunAt,omitempty"`
	LastRunAt    *time.Time `json:"lastRunAt,omitempty"`
	LastResult   string     `json:"lastResult,omitempty"` // success | error | skipped
	LastError    string     `json:"lastError,omitempty"`
	RunCount     int        `json:"runCount"`
}

// Expiry overrides a machine's provider-side TTL. A nil ExpiresAt means
// "never expire".
type Expiry struct {
	Project   string     `json:"project"`
	MachineID string     `json:"machineId"`
	ExpiresAt *time.Time `json:"expiresAt"`
	SetBy     string     `json:"setBy,omitempty"`
	SetAt     time.Time  `json:"setAt"`
}

type fileShape struct {
	Schedules []Schedule `json:"schedules"`
	Expiries  []Expiry   `json:"expiries"`
}

// Store persists schedules and expiry overrides. Safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	path     string
	now      func() time.Time
	items    map[string]*Schedule
	expiries map[string]Expiry // project/machine
}

// NewStore loads path ("" = memory only). now may be nil (time.Now).
func NewStore(path string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	s := &Store{path: path, now: now, items: map[string]*Schedule{}, expiries: map[string]Expiry{}}
	if path == "" {
		return s, nil
	}
	// #nosec G304 -- operator-configured path
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var f fileShape
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse schedules file: %w", err)
	}
	for i := range f.Schedules {
		sc := f.Schedules[i]
		s.items[sc.ID] = &sc
	}
	for _, e := range f.Expiries {
		s.expiries[e.Project+"/"+e.MachineID] = e
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	f := fileShape{Schedules: []Schedule{}, Expiries: []Expiry{}}
	for _, sc := range s.items {
		f.Schedules = append(f.Schedules, *sc)
	}
	sort.Slice(f.Schedules, func(i, j int) bool { return f.Schedules[i].CreatedAt.Before(f.Schedules[j].CreatedAt) })
	for _, e := range s.expiries {
		f.Expiries = append(f.Expiries, e)
	}
	sort.Slice(f.Expiries, func(i, j int) bool { return f.Expiries[i].SetAt.Before(f.Expiries[j].SetAt) })
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// compute validates cron/timezone and returns the next run after t.
func compute(cron, tz string, after time.Time) (*time.Time, error) {
	c, err := ParseCron(cron)
	if err != nil {
		return nil, err
	}
	loc, err := LoadLocation(tz)
	if err != nil {
		return nil, err
	}
	n := c.Next(after.In(loc))
	if n.IsZero() {
		return nil, errors.New("cron expression never fires")
	}
	u := n.UTC()
	return &u, nil
}

// Create validates and stores a schedule, computing its first run.
func (s *Store) Create(sc Schedule) (Schedule, error) {
	if !ValidAction(sc.Action) {
		return Schedule{}, errors.New("action must be one of start, stop, restart, snapshot")
	}
	if strings.TrimSpace(sc.MachineID) == "" {
		return Schedule{}, errors.New("machineId is required")
	}
	if len(sc.SnapshotName) > 63 {
		return Schedule{}, errors.New("snapshotName must be at most 63 characters")
	}
	if sc.Timezone == "" {
		sc.Timezone = "UTC"
	}
	now := s.now()
	next, err := compute(sc.Cron, sc.Timezone, now)
	if err != nil {
		return Schedule{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, x := range s.items {
		if x.Project == sc.Project {
			n++
		}
	}
	if n >= MaxPerProject {
		return Schedule{}, ErrLimit
	}
	sc.ID, sc.Enabled, sc.CreatedAt, sc.UpdatedAt, sc.NextRunAt = id.New(), true, now, now, next
	s.items[sc.ID] = &sc
	if err := s.saveLocked(); err != nil {
		delete(s.items, sc.ID)
		return Schedule{}, err
	}
	return sc, nil
}

// Patch carries optional updates.
type Patch struct {
	Action       *string
	Cron         *string
	Timezone     *string
	SnapshotName *string
	Enabled      *bool
}

// Update applies p and recomputes the next run.
func (s *Store) Update(sid string, p Patch) (Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	cur, ok := s.items[sid]
	if !ok {
		return Schedule{}, ErrNotFound
	}
	next := *cur
	if p.Action != nil {
		if !ValidAction(*p.Action) {
			return Schedule{}, errors.New("action must be one of start, stop, restart, snapshot")
		}
		next.Action = *p.Action
	}
	if p.Cron != nil {
		next.Cron = *p.Cron
	}
	if p.Timezone != nil {
		next.Timezone = *p.Timezone
		if next.Timezone == "" {
			next.Timezone = "UTC"
		}
	}
	if p.SnapshotName != nil {
		next.SnapshotName = *p.SnapshotName
	}
	if p.Enabled != nil {
		next.Enabled = *p.Enabled
	}
	n, err := compute(next.Cron, next.Timezone, s.now())
	if err != nil {
		return Schedule{}, err
	}
	next.NextRunAt = n
	next.UpdatedAt = s.now()
	old := *cur
	*cur = next
	if err := s.saveLocked(); err != nil {
		*cur = old
		return Schedule{}, err
	}
	return next, nil
}

// Delete removes a schedule.
func (s *Store) Delete(sid string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.items[sid]
	if !ok {
		return ErrNotFound
	}
	delete(s.items, sid)
	if err := s.saveLocked(); err != nil {
		s.items[sid] = old
		return err
	}
	return nil
}

// Get returns one schedule.
func (s *Store) Get(sid string) (Schedule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.items[sid]
	if !ok {
		return Schedule{}, ErrNotFound
	}
	return *sc, nil
}

// List returns schedules oldest first, optionally narrowed by project,
// machine and a visibility predicate.
func (s *Store) List(project, machineID string, visible func(string) bool) []Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Schedule{}
	for _, sc := range s.items {
		if (project == "" || sc.Project == project) && (machineID == "" || sc.MachineID == machineID) && (visible == nil || visible(sc.Project)) {
			out = append(out, *sc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

// Due returns enabled schedules whose next run is at or before now.
func (s *Store) Due(now time.Time) []Schedule {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Schedule
	for _, sc := range s.items {
		if sc.Enabled && sc.NextRunAt != nil && !sc.NextRunAt.After(now) {
			out = append(out, *sc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].NextRunAt.Before(*out[j].NextRunAt) })
	return out
}

// MarkRun records an execution outcome and advances NextRunAt past now.
func (s *Store) MarkRun(sid string, ranAt time.Time, result, errMsg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.items[sid]
	if !ok {
		return
	}
	sc.LastRunAt = &ranAt
	sc.LastResult, sc.LastError = result, errMsg
	if result != "skipped" {
		sc.RunCount++
	}
	if n, err := compute(sc.Cron, sc.Timezone, ranAt); err == nil {
		sc.NextRunAt = n
	} else {
		sc.NextRunAt, sc.Enabled = nil, false
	}
	_ = s.saveLocked()
}

// ForgetMachine deletes every schedule and expiry override for a machine
// (called when it is deleted); it returns how many schedules were removed.
func (s *Store) ForgetMachine(project, machineID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, sc := range s.items {
		if sc.Project == project && sc.MachineID == machineID {
			delete(s.items, id)
			n++
		}
	}
	delete(s.expiries, project+"/"+machineID)
	_ = s.saveLocked()
	return n
}

// SetExpiry overrides the machine's expiry; at == nil means never expire.
func (s *Store) SetExpiry(project, machineID string, at *time.Time, by string) (Expiry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := project + "/" + machineID
	old, had := s.expiries[k]
	e := Expiry{Project: project, MachineID: machineID, ExpiresAt: at, SetBy: by, SetAt: s.now()}
	s.expiries[k] = e
	if err := s.saveLocked(); err != nil {
		if had {
			s.expiries[k] = old
		} else {
			delete(s.expiries, k)
		}
		return Expiry{}, err
	}
	return e, nil
}

// Expiry returns the override for a machine. ok=false means none is set;
// ok=true with a nil time means "never expires".
func (s *Store) Expiry(project, machineID string) (*time.Time, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.expiries[project+"/"+machineID]
	if !ok {
		return nil, false
	}
	return e.ExpiresAt, true
}

// ExpiryCount returns the number of overrides (for tests/metrics).
func (s *Store) ExpiryCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.expiries)
}
