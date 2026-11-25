// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package protect

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestProtectPersistsAndClears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "protections.json")
	s, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Set("lab", "m1", "prod database", "alice"); err != nil {
		t.Fatal(err)
	}
	again, err := NewStore(path)
	if err != nil {
		t.Fatal(err)
	}
	p, ok := again.Get("lab", "m1")
	if !ok || p.Reason != "prod database" || p.SetBy != "alice" || p.SetAt.IsZero() {
		t.Fatalf("reloaded = %+v %v", p, ok)
	}
	if _, ok := again.Get("default", "m1"); ok {
		t.Fatal("protection leaked across projects")
	}
	if _, err := again.BeginDelete("lab", "m1"); !errors.Is(err, ErrProtected) || !strings.Contains(err.Error(), "prod database") {
		t.Fatalf("BeginDelete = %v", err)
	}
	if err := again.Clear("lab", "m1"); err != nil {
		t.Fatal(err)
	}
	if err := again.Clear("lab", "m1"); err != nil {
		t.Fatalf("second clear: %v", err)
	}
	third, _ := NewStore(path)
	if _, ok := third.Get("lab", "m1"); ok {
		t.Fatal("clear was not persisted")
	}
}

func TestCorruptFileIsAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "protections.json")
	if err := os.WriteFile(path, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewStore(path); err == nil {
		t.Fatal("corrupt file was accepted: every machine would be unprotected")
	}
}

func TestFailedSaveRollsBack(t *testing.T) {
	dir := t.TempDir()
	blocker := filepath.Join(dir, "file")
	if err := os.WriteFile(blocker, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	s := &Store{path: filepath.Join(blocker, "p.json"), items: map[string]Protection{}, deleting: map[string]int{}}
	if _, err := s.Set("lab", "m1", "", "a"); err == nil {
		t.Fatal("save into a non-directory succeeded")
	}
	if _, ok := s.Get("lab", "m1"); ok {
		t.Fatal("unsaved protection stayed in memory")
	}
}

func TestReasonValidation(t *testing.T) {
	s, _ := NewStore("")
	if _, err := s.Set("lab", "m1", strings.Repeat("é", MaxReasonRunes), "a"); err != nil {
		t.Fatalf("256 runes rejected: %v", err)
	}
	for _, bad := range []string{strings.Repeat("x", MaxReasonRunes+1), "\xff"} {
		if _, err := s.Set("lab", "m2", bad, "a"); !errors.Is(err, ErrReason) {
			t.Errorf("%q: %v", bad, err)
		}
	}
}

func TestSetIsRefusedWhileDeleting(t *testing.T) {
	s, _ := NewStore("")
	r1, err := s.BeginDelete("lab", "m1")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := s.BeginDelete("lab", "m1")
	if err != nil {
		t.Fatal(err)
	}
	r1()
	r1() // releasing twice must not release the other claim
	if _, err := s.Set("lab", "m1", "", "a"); !errors.Is(err, ErrDeleting) {
		t.Fatalf("Set during delete = %v", err)
	}
	r2()
	if _, err := s.Set("lab", "m1", "", "a"); err != nil {
		t.Fatalf("Set after release = %v", err)
	}
}

func TestConcurrentAccess(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "p.json"))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				if release, err := s.BeginDelete("lab", "m"); err == nil {
					release()
				}
				_, _ = s.Set("lab", "m", "", "a")
				s.Get("lab", "m")
				_ = s.Clear("lab", "m")
			}
		}()
	}
	wg.Wait()
}
