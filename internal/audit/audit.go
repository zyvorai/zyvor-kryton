// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

// Package audit keeps Kryton's append-only record of every mutating API
// call and background action (who, project, action, machine, result,
// request id). Entries live in a bounded in-memory ring for querying via
// GET /api/v1/audit and can additionally be appended to a JSONL file that
// is size-rotated. There is deliberately no update or delete operation.
package audit

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zyvorai/kryton/internal/id"
)

// Result classifies the outcome of an audited call.
const (
	ResultSuccess = "success"
	ResultDenied  = "denied" // 401/403/429
	ResultError   = "error"  // any other >= 400
)

// Entry is one audit record.
type Entry struct {
	Seq       uint64    `json:"seq"`
	ID        string    `json:"id"`
	Time      time.Time `json:"time"`
	Actor     string    `json:"actor"`
	Role      string    `json:"role,omitempty"`
	Project   string    `json:"project,omitempty"`
	Action    string    `json:"action"`
	MachineID string    `json:"machineId,omitempty"`
	Method    string    `json:"method,omitempty"`
	Path      string    `json:"path,omitempty"`
	Status    int       `json:"status,omitempty"`
	Result    string    `json:"result"`
	RequestID string    `json:"requestId,omitempty"`
	Remote    string    `json:"remote,omitempty"`
	Detail    string    `json:"detail,omitempty"`
}

// ResultForStatus maps an HTTP status to a Result string.
func ResultForStatus(status int) string {
	switch {
	case status < 400:
		return ResultSuccess
	case status == 401 || status == 403 || status == 429:
		return ResultDenied
	default:
		return ResultError
	}
}

// Config configures a Store.
type Config struct {
	// MaxEntries bounds the in-memory ring (default 10000).
	MaxEntries int
	// File, when set, is the JSONL sink; MaxFileBytes (default 10 MiB)
	// triggers rotation to File.1 ... File.MaxFiles (default 5).
	File         string
	MaxFileBytes int64
	MaxFiles     int
}

// Store is the append-only audit log. Safe for concurrent use.
type Store struct {
	mu      sync.RWMutex
	entries []Entry // oldest first
	max     int
	seq     uint64
	sink    *rotatingFile
}

// New opens a Store, loading any existing entries from the sink file (and
// its newest rotation) so history survives restarts.
func New(cfg Config) (*Store, error) {
	if cfg.MaxEntries < 1 {
		cfg.MaxEntries = 10000
	}
	if cfg.MaxFileBytes <= 0 {
		cfg.MaxFileBytes = 10 << 20
	}
	if cfg.MaxFiles <= 0 {
		cfg.MaxFiles = 5
	}
	s := &Store{max: cfg.MaxEntries}
	if cfg.File != "" {
		var loaded []Entry
		for _, p := range []string{cfg.File + ".1", cfg.File} {
			es, err := readJSONL(p)
			if err != nil {
				return nil, err
			}
			loaded = append(loaded, es...)
		}
		if len(loaded) > s.max {
			loaded = loaded[len(loaded)-s.max:]
		}
		s.entries = loaded
		for _, e := range loaded {
			if e.Seq > s.seq {
				s.seq = e.Seq
			}
		}
		rf, err := openRotating(cfg.File, cfg.MaxFileBytes, cfg.MaxFiles)
		if err != nil {
			return nil, err
		}
		s.sink = rf
	}
	return s, nil
}

// Close flushes and closes the file sink.
func (s *Store) Close() error {
	if s == nil || s.sink == nil {
		return nil
	}
	return s.sink.close()
}

// Record appends e (assigning Seq, ID and Time when unset) and returns it.
// A file-sink failure is returned but the entry is still retained in memory.
func (s *Store) Record(e Entry) (Entry, error) {
	if s == nil {
		return e, nil
	}
	if e.Time.IsZero() {
		e.Time = time.Now().UTC()
	}
	if e.ID == "" {
		e.ID = id.New()
	}
	if e.Result == "" {
		e.Result = ResultForStatus(e.Status)
	}
	s.mu.Lock()
	s.seq++
	e.Seq = s.seq
	s.entries = append(s.entries, e)
	if len(s.entries) > s.max {
		s.entries = append([]Entry(nil), s.entries[len(s.entries)-s.max:]...)
	}
	s.mu.Unlock()
	if s.sink != nil {
		b, err := json.Marshal(e)
		if err == nil {
			err = s.sink.write(append(b, '\n'))
		}
		return e, err
	}
	return e, nil
}

// Filter selects entries for Query; zero fields match everything.
type Filter struct {
	Project   string
	Actor     string
	Action    string // exact, or prefix when it ends in "*"
	MachineID string
	Result    string
	Since     time.Time
	Until     time.Time
	// Visible, when set, hides entries whose project it rejects.
	Visible func(project string) bool
	Limit   int
	Cursor  string
}

// ErrBadCursor is returned by Query for a malformed cursor.
var ErrBadCursor = errors.New("invalid cursor")

func (f Filter) match(e Entry) bool {
	if f.Visible != nil && !f.Visible(e.Project) {
		return false
	}
	if f.Project != "" && e.Project != f.Project {
		return false
	}
	if f.Actor != "" && e.Actor != f.Actor {
		return false
	}
	if f.MachineID != "" && e.MachineID != f.MachineID {
		return false
	}
	if f.Result != "" && e.Result != f.Result {
		return false
	}
	if f.Action != "" {
		if strings.HasSuffix(f.Action, "*") {
			if !strings.HasPrefix(e.Action, strings.TrimSuffix(f.Action, "*")) {
				return false
			}
		} else if e.Action != f.Action {
			return false
		}
	}
	if !f.Since.IsZero() && e.Time.Before(f.Since) {
		return false
	}
	if !f.Until.IsZero() && e.Time.After(f.Until) {
		return false
	}
	return true
}

// Query returns matching entries newest first, at most Limit (default 50,
// max 500), plus a cursor for the next page ("" on the last page).
func (s *Store) Query(f Filter) ([]Entry, string, error) {
	limit := f.Limit
	if limit < 1 {
		limit = 50
	}
	if limit > 500 {
		limit = 500
	}
	before := uint64(0)
	if f.Cursor != "" {
		b, err := base64.RawURLEncoding.DecodeString(f.Cursor)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		n, err := strconv.ParseUint(string(b), 10, 64)
		if err != nil {
			return nil, "", ErrBadCursor
		}
		before = n
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Entry, 0, limit)
	next := ""
	for i := len(s.entries) - 1; i >= 0; i-- {
		e := s.entries[i]
		if before != 0 && e.Seq >= before {
			continue
		}
		if !f.match(e) {
			continue
		}
		if len(out) == limit {
			// At least one more match exists: hand back a cursor.
			next = base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatUint(out[len(out)-1].Seq, 10)))
			break
		}
		out = append(out, e)
	}
	return out, next, nil
}

// Len returns the number of entries currently held in memory.
func (s *Store) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.entries)
}

func readJSONL(path string) ([]Entry, error) {
	// #nosec G304 -- path is operator configuration (KRYTON_AUDIT_FILE)
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.ID != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}

// rotatingFile is an append-only file that rotates by size.
type rotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	maxFiles int
	f        *os.File
	size     int64
}

func openRotating(path string, maxBytes int64, maxFiles int) (*rotatingFile, error) {
	r := &rotatingFile{path: path, maxBytes: maxBytes, maxFiles: maxFiles}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) open() error {
	// #nosec G304 -- operator-configured audit path
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open audit file: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	r.f, r.size = f, st.Size()
	return nil
}

func (r *rotatingFile) write(b []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size > 0 && r.size+int64(len(b)) > r.maxBytes {
		if err := r.rotate(); err != nil {
			return err
		}
	}
	n, err := r.f.Write(b)
	r.size += int64(n)
	return err
}

func (r *rotatingFile) rotate() error {
	_ = r.f.Close()
	// path.(max-1) is the oldest kept; drop anything beyond.
	_ = os.Remove(fmt.Sprintf("%s.%d", r.path, r.maxFiles))
	for i := r.maxFiles - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", r.path, i), fmt.Sprintf("%s.%d", r.path, i+1))
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil {
		return err
	}
	return r.open()
}

func (r *rotatingFile) close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.f.Close()
}
