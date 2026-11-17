// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

// Package quota defines per-project resource limits (machines, vCPU,
// memory, disk), their persistence, and the create-time check that
// enforces them. A limit of 0 means "unlimited".
package quota

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/zyvorai/kryton/internal/model"
)

// Limits caps what one project may hold. Zero means unlimited.
type Limits struct {
	MaxMachines  int `json:"maxMachines"`
	MaxVCPU      int `json:"maxVCPU"`
	MaxMemoryMiB int `json:"maxMemoryMiB"`
	MaxDiskGiB   int `json:"maxDiskGiB"`
}

// IsZero reports whether no limit is set.
func (l Limits) IsZero() bool { return l == Limits{} }

// Validate rejects negative limits.
func (l Limits) Validate() error {
	if l.MaxMachines < 0 || l.MaxVCPU < 0 || l.MaxMemoryMiB < 0 || l.MaxDiskGiB < 0 {
		return errors.New("quota limits must be zero (unlimited) or positive")
	}
	return nil
}

// Usage is the resources a project currently holds (or a request would add).
type Usage struct {
	Machines  int `json:"machines"`
	VCPU      int `json:"vCPU"`
	MemoryMiB int `json:"memoryMiB"`
	DiskGiB   int `json:"diskGiB"`
}

// UsageOf sums machines, vCPU, memory and disk (primary plus extra dockur
// disks). Machines being deleted no longer count; stopped machines do, since
// they still hold their allocation.
func UsageOf(ms []model.Machine) Usage {
	var u Usage
	for _, m := range ms {
		if m.State == model.StateDeleting {
			continue
		}
		u.Machines++
		u.VCPU += m.Spec.Compute.CPU
		u.MemoryMiB += m.Spec.Compute.MemoryMiB
		u.DiskGiB += DiskOf(m.Spec)
	}
	return u
}

// DiskOf is the total disk a spec asks for.
func DiskOf(spec model.MachineSpec) int {
	n := spec.Disk.SizeGiB
	if spec.Dockur != nil {
		for _, d := range spec.Dockur.ExtraDisksGiB {
			n += d
		}
	}
	return n
}

// Violation describes the first limit a create would break.
type Violation struct {
	Resource  string // machines | vCPU | memoryMiB | diskGiB
	Limit     int
	Used      int
	Requested int
	// Hard is true when the request alone exceeds the limit, so deleting
	// other machines can never make it fit (HTTP 403); otherwise it is a
	// conflict with current usage (HTTP 409).
	Hard bool
}

func (v *Violation) Error() string {
	return fmt.Sprintf("project quota exceeded: %s limit %d, in use %d, requested %d", v.Resource, v.Limit, v.Used, v.Requested)
}

// Check returns nil when adding req to used stays within l, else the first
// violated dimension.
func Check(l Limits, used, req Usage) *Violation {
	for _, d := range []struct {
		name         string
		limit, u, rq int
	}{
		{"machines", l.MaxMachines, used.Machines, req.Machines},
		{"vCPU", l.MaxVCPU, used.VCPU, req.VCPU},
		{"memoryMiB", l.MaxMemoryMiB, used.MemoryMiB, req.MemoryMiB},
		{"diskGiB", l.MaxDiskGiB, used.DiskGiB, req.DiskGiB},
	} {
		if d.limit > 0 && d.u+d.rq > d.limit {
			return &Violation{Resource: d.name, Limit: d.limit, Used: d.u, Requested: d.rq, Hard: d.rq > d.limit}
		}
	}
	return nil
}

// RequestOf converts a machine spec into the Usage it would add.
func RequestOf(spec model.MachineSpec) Usage {
	return Usage{Machines: 1, VCPU: spec.Compute.CPU, MemoryMiB: spec.Compute.MemoryMiB, DiskGiB: DiskOf(spec)}
}

type fileShape struct {
	Default  *Limits           `json:"default,omitempty"`
	Projects map[string]Limits `json:"projects,omitempty"`
}

// Store holds the default and per-project limits, persisted as JSON.
type Store struct {
	mu       sync.RWMutex
	path     string
	defaults Limits
	projects map[string]Limits
}

// NewStore loads path (may be "" for memory-only; a missing file is fine).
// envDefaults seeds the default limits when the file does not define them.
func NewStore(path string, envDefaults Limits) (*Store, error) {
	s := &Store{path: path, defaults: envDefaults, projects: map[string]Limits{}}
	if err := envDefaults.Validate(); err != nil {
		return nil, err
	}
	if path == "" {
		return s, nil
	}
	// #nosec G304 -- operator-configured path
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read quotas file: %w", err)
	}
	var f fileShape
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse quotas file: %w", err)
	}
	if f.Default != nil {
		if err := f.Default.Validate(); err != nil {
			return nil, err
		}
		s.defaults = *f.Default
	}
	for p, l := range f.Projects {
		if err := l.Validate(); err != nil {
			return nil, fmt.Errorf("project %q: %w", p, err)
		}
		s.projects[p] = l
	}
	return s, nil
}

// Get returns the effective limits for project: its own entry, else the default.
func (s *Store) Get(project string) Limits {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if l, ok := s.projects[project]; ok {
		return l
	}
	return s.defaults
}

// Explicit reports whether project has its own entry.
func (s *Store) Explicit(project string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.projects[project]
	return ok
}

// Set stores limits for project and persists. All-zero limits are kept as an
// explicit "unlimited" override of the default.
func (s *Store) Set(project string, l Limits) error {
	if err := l.Validate(); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.projects[project]
	s.projects[project] = l
	if err := s.persist(); err != nil {
		if had {
			s.projects[project] = prev
		} else {
			delete(s.projects, project)
		}
		return err
	}
	return nil
}

// Delete removes the project override so it falls back to the default.
func (s *Store) Delete(project string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, had := s.projects[project]
	delete(s.projects, project)
	if err := s.persist(); err != nil {
		if had {
			s.projects[project] = prev
		}
		return err
	}
	return nil
}

// Projects lists projects with explicit entries, sorted.
func (s *Store) Projects() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]string, 0, len(s.projects))
	for p := range s.projects {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func (s *Store) persist() error {
	if s.path == "" {
		return nil
	}
	d := s.defaults
	b, err := json.MarshalIndent(fileShape{Default: &d, Projects: s.projects}, "", "  ")
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
