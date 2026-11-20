// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package quota

import (
	"path/filepath"
	"testing"

	"github.com/zyvorai/kryton/internal/model"
)

func machine(cpu, mem, disk int, state model.MachineState) model.Machine {
	return model.Machine{State: state, Spec: model.MachineSpec{Compute: model.ComputeSpec{CPU: cpu, MemoryMiB: mem}, Disk: model.DiskSpec{SizeGiB: disk}}}
}

func TestUsageOfIgnoresDeletingAndCountsExtraDisks(t *testing.T) {
	m := machine(2, 4096, 50, model.StateRunning)
	m.Spec.Dockur = &model.DockurOptions{ExtraDisksGiB: []int{10, 20}}
	u := UsageOf([]model.Machine{m, machine(8, 8192, 100, model.StateDeleting), machine(1, 1024, 16, model.StateStopped)})
	want := Usage{Machines: 2, VCPU: 3, MemoryMiB: 5120, DiskGiB: 96}
	if u != want {
		t.Fatalf("usage=%+v want %+v", u, want)
	}
}

func TestCheck(t *testing.T) {
	l := Limits{MaxMachines: 3, MaxVCPU: 8, MaxMemoryMiB: 16384, MaxDiskGiB: 200}
	used := Usage{Machines: 2, VCPU: 6, MemoryMiB: 8192, DiskGiB: 100}
	if v := Check(l, used, Usage{Machines: 1, VCPU: 2, MemoryMiB: 8192, DiskGiB: 100}); v != nil {
		t.Fatalf("exact fit should pass: %v", v)
	}
	v := Check(l, used, Usage{Machines: 1, VCPU: 4, MemoryMiB: 1024, DiskGiB: 16})
	if v == nil || v.Resource != "vCPU" || v.Hard || v.Limit != 8 || v.Used != 6 || v.Requested != 4 {
		t.Fatalf("expected soft vCPU violation, got %+v", v)
	}
	v = Check(l, Usage{}, Usage{Machines: 1, VCPU: 16, MemoryMiB: 1024, DiskGiB: 16})
	if v == nil || !v.Hard || v.Resource != "vCPU" {
		t.Fatalf("expected hard vCPU violation, got %+v", v)
	}
	v = Check(l, Usage{Machines: 3}, Usage{Machines: 1})
	if v == nil || v.Resource != "machines" || v.Hard {
		t.Fatalf("machine count: %+v", v)
	}
	if Check(Limits{}, Usage{Machines: 999}, Usage{Machines: 1, VCPU: 999}) != nil {
		t.Fatal("zero limits mean unlimited")
	}
}

func TestStorePersistenceAndFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q", "quotas.json")
	s, err := NewStore(path, Limits{MaxMachines: 10})
	if err != nil {
		t.Fatal(err)
	}
	if s.Get("lab").MaxMachines != 10 || s.Explicit("lab") {
		t.Fatal("default should apply")
	}
	if err := s.Set("lab", Limits{MaxMachines: 2, MaxVCPU: 4}); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("bad", Limits{MaxVCPU: -1}); err == nil {
		t.Fatal("negative limit accepted")
	}
	s2, err := NewStore(path, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if got := s2.Get("lab"); got.MaxMachines != 2 || got.MaxVCPU != 4 {
		t.Fatalf("not persisted: %+v", got)
	}
	if s2.Get("other").MaxMachines != 10 {
		t.Fatal("persisted default lost")
	}
	if err := s2.Delete("lab"); err != nil {
		t.Fatal(err)
	}
	if s2.Get("lab").MaxMachines != 10 {
		t.Fatal("delete should fall back to default")
	}
}
