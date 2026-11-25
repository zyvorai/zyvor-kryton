// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

// Package protect stores per-machine delete protection. A protected machine
// cannot be deleted through the API, by a bulk delete or by TTL expiry until
// the protection is removed. It guards against accidents, not against an
// operator who is allowed to remove it. State is persisted to a JSON file.
package protect

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

// MaxReasonRunes caps the free-text reason.
const MaxReasonRunes = 256

var (
	// ErrProtected is returned when a delete is attempted on a protected machine.
	ErrProtected = errors.New("machine is delete protected")
	// ErrDeleting is returned when protection is requested for a machine whose
	// delete is already running: it would not be honoured.
	ErrDeleting = errors.New("machine is being deleted")
	// ErrReason rejects an over-long or non UTF-8 reason.
	ErrReason = errors.New("reason must be valid text of at most 256 characters")
)

// Protection marks one machine as protected.
type Protection struct {
	Project   string    `json:"project"`
	MachineID string    `json:"machineId"`
	Reason    string    `json:"reason,omitempty"`
	SetBy     string    `json:"setBy,omitempty"`
	SetAt     time.Time `json:"setAt"`
}

type fileShape struct {
	Protections []Protection `json:"protections"`
}

// Store holds the protections. Safe for concurrent use.
type Store struct {
	mu       sync.Mutex
	path     string
	items    map[string]Protection
	deleting map[string]int
}

func key(project, machineID string) string { return project + "/" + machineID }

// NewStore loads path ("" = memory only). A file that cannot be parsed is an
// error: starting with an empty store would silently unprotect every machine.
func NewStore(path string) (*Store, error) {
	s := &Store{path: path, items: map[string]Protection{}, deleting: map[string]int{}}
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
		return nil, fmt.Errorf("parse protections file: %w", err)
	}
	for _, p := range f.Protections {
		s.items[key(p.Project, p.MachineID)] = p
	}
	return s, nil
}

func (s *Store) saveLocked() error {
	if s.path == "" {
		return nil
	}
	f := fileShape{Protections: []Protection{}}
	for _, p := range s.items {
		f.Protections = append(f.Protections, p)
	}
	sort.Slice(f.Protections, func(i, j int) bool {
		return key(f.Protections[i].Project, f.Protections[i].MachineID) < key(f.Protections[j].Project, f.Protections[j].MachineID)
	})
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

// Set protects a machine (replacing any previous reason).
func (s *Store) Set(project, machineID, reason, by string) (Protection, error) {
	if !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > MaxReasonRunes {
		return Protection{}, ErrReason
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(project, machineID)
	if s.deleting[k] > 0 {
		return Protection{}, ErrDeleting
	}
	old, had := s.items[k]
	p := Protection{Project: project, MachineID: machineID, Reason: reason, SetBy: by, SetAt: time.Now().UTC()}
	s.items[k] = p
	if err := s.saveLocked(); err != nil {
		if had {
			s.items[k] = old
		} else {
			delete(s.items, k)
		}
		return Protection{}, err
	}
	return p, nil
}

// Clear removes the protection; clearing an unprotected machine is a no-op.
func (s *Store) Clear(project, machineID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(project, machineID)
	old, had := s.items[k]
	if !had {
		return nil
	}
	delete(s.items, k)
	if err := s.saveLocked(); err != nil {
		s.items[k] = old
		return err
	}
	return nil
}

// Get returns the protection of a machine, if any.
func (s *Store) Get(project, machineID string) (Protection, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.items[key(project, machineID)]
	return p, ok
}

// BeginDelete claims a machine for deletion. It fails with ErrProtected when
// the machine is protected. Until release is called, Set refuses the machine,
// so protection can never be acknowledged for a delete already in flight.
func (s *Store) BeginDelete(project, machineID string) (release func(), err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := key(project, machineID)
	if p, ok := s.items[k]; ok {
		if p.Reason != "" {
			return nil, fmt.Errorf("%w: %s", ErrProtected, p.Reason)
		}
		return nil, ErrProtected
	}
	s.deleting[k]++
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			defer s.mu.Unlock()
			if s.deleting[k]--; s.deleting[k] <= 0 {
				delete(s.deleting, k)
			}
		})
	}, nil
}
