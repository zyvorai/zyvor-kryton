// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecordQueryFiltersAndPagination(t *testing.T) {
	s, err := New(Config{MaxEntries: 100})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 7; i++ {
		proj := "a"
		if i%2 == 1 {
			proj = "b"
		}
		status := 200
		if i == 6 {
			status = 403
		}
		_, _ = s.Record(Entry{Time: base.Add(time.Duration(i) * time.Minute), Actor: "alice", Project: proj, Action: "machine.create", MachineID: "m" + proj, Status: status})
	}
	_, _ = s.Record(Entry{Time: base.Add(time.Hour), Actor: "bob", Project: "a", Action: "snapshot.create", Status: 201})

	all, next, err := s.Query(Filter{Limit: 3})
	if err != nil || len(all) != 3 || next == "" {
		t.Fatalf("page1 len=%d next=%q err=%v", len(all), next, err)
	}
	if all[0].Action != "snapshot.create" {
		t.Fatalf("not newest first: %+v", all[0])
	}
	seen := map[uint64]bool{}
	cursor := ""
	for pages := 0; pages < 10; pages++ {
		items, nc, err := s.Query(Filter{Limit: 3, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range items {
			if seen[e.Seq] {
				t.Fatalf("duplicate seq %d across pages", e.Seq)
			}
			seen[e.Seq] = true
		}
		if nc == "" {
			break
		}
		cursor = nc
	}
	if len(seen) != 8 {
		t.Fatalf("paged %d entries, want 8", len(seen))
	}

	got, _, _ := s.Query(Filter{Project: "b"})
	if len(got) != 3 {
		t.Fatalf("project filter: %d", len(got))
	}
	got, _, _ = s.Query(Filter{Actor: "bob"})
	if len(got) != 1 {
		t.Fatalf("actor filter: %d", len(got))
	}
	got, _, _ = s.Query(Filter{Action: "snapshot.*"})
	if len(got) != 1 {
		t.Fatalf("prefix filter: %d", len(got))
	}
	got, _, _ = s.Query(Filter{Result: ResultDenied})
	if len(got) != 1 || got[0].Status != 403 {
		t.Fatalf("result filter: %+v", got)
	}
	got, _, _ = s.Query(Filter{Since: base.Add(5 * time.Minute), Until: base.Add(30 * time.Minute)})
	if len(got) != 2 {
		t.Fatalf("time filter: %d", len(got))
	}
	got, _, _ = s.Query(Filter{Visible: func(p string) bool { return p == "a" }})
	for _, e := range got {
		if e.Project != "a" {
			t.Fatalf("visibility leak: %+v", e)
		}
	}
	if _, _, err := s.Query(Filter{Cursor: "!!"}); err != ErrBadCursor {
		t.Fatalf("expected bad cursor, got %v", err)
	}
}

func TestRingBound(t *testing.T) {
	s, _ := New(Config{MaxEntries: 5})
	for i := 0; i < 20; i++ {
		_, _ = s.Record(Entry{Actor: "x", Action: "a", Status: 200})
	}
	if s.Len() != 5 {
		t.Fatalf("len=%d", s.Len())
	}
	items, _, _ := s.Query(Filter{Limit: 500})
	if items[0].Seq != 20 || items[4].Seq != 16 {
		t.Fatalf("unexpected retained seqs: %d..%d", items[0].Seq, items[4].Seq)
	}
}

func TestFileSinkRotationAndReload(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.jsonl")
	s, err := New(Config{File: path, MaxFileBytes: 600, MaxFiles: 2, MaxEntries: 1000})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 30; i++ {
		if _, err := s.Record(Entry{Actor: "alice", Project: "p", Action: "machine.start", Status: 200, RequestID: "req"}); err != nil {
			t.Fatal(err)
		}
	}
	_ = s.Close()
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("expected rotation: %v", err)
	}
	if _, err := os.Stat(path + ".3"); err == nil {
		t.Fatal("kept more rotations than MaxFiles")
	}
	st, _ := os.Stat(path)
	if st.Size() > 600+300 {
		t.Fatalf("active file not bounded: %d", st.Size())
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("perm=%v", st.Mode().Perm())
	}
	raw, _ := os.ReadFile(path)
	if !strings.Contains(string(raw), `"action":"machine.start"`) {
		t.Fatalf("jsonl missing action: %s", raw)
	}

	// Reload keeps history and continues sequence numbers.
	s2, err := New(Config{File: path, MaxFileBytes: 600, MaxFiles: 2})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s2.Close() }()
	if s2.Len() == 0 {
		t.Fatal("no entries reloaded")
	}
	e, _ := s2.Record(Entry{Actor: "x", Action: "y", Status: 200})
	if e.Seq <= 30-uint64(s2.Len()) || e.Seq != 31 {
		t.Fatalf("seq did not continue: %d", e.Seq)
	}
}

func TestResultForStatus(t *testing.T) {
	cases := map[int]string{200: ResultSuccess, 204: ResultSuccess, 401: ResultDenied, 403: ResultDenied, 429: ResultDenied, 400: ResultError, 409: ResultError, 500: ResultError}
	for code, want := range cases {
		if got := ResultForStatus(code); got != want {
			t.Errorf("%d: got %s want %s", code, got, want)
		}
	}
}
