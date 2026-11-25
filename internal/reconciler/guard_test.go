// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/zyvorai/kryton/internal/audit"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/protect"
)

func TestProtectedMachinesSurviveExpiry(t *testing.T) {
	fp := &fakeProvider{machines: map[string][]model.Machine{"default": {
		{ID: "keep", ExpiresAt: expiresAt(-time.Hour)},
		{ID: "drop", ExpiresAt: expiresAt(-time.Hour)},
	}}}
	guard, _ := protect.NewStore("")
	if _, err := guard.Set("default", "keep", "demo box", "op"); err != nil {
		t.Fatal(err)
	}
	bus, err := events.New(20, "", "", "", discardLogger())
	if err != nil {
		t.Fatal(err)
	}
	au, _ := audit.New(audit.Config{})
	r := TTL{Provider: fp, Projects: []string{"default"}, Events: bus, Log: discardLogger(), Guard: guard, Audit: au, held: map[string]bool{}}
	r.runOnce(context.Background())
	r.runOnce(context.Background())
	for _, d := range fp.deleted {
		if d != "default/drop" {
			t.Fatalf("deleted %v", fp.deleted)
		}
	}
	blocked := 0
	for _, e := range bus.List(50) {
		if e.Type == "io.kryton.machine.expiry.blocked" {
			blocked++
		}
	}
	if blocked != 1 {
		t.Fatalf("blocked events = %d, want one", blocked)
	}
	denied, _, err := au.Query(audit.Filter{MachineID: "keep", Result: audit.ResultDenied})
	if err != nil || len(denied) != 1 {
		t.Fatalf("denied audit entries = %v %v", denied, err)
	}
	if err := guard.Clear("default", "keep"); err != nil {
		t.Fatal(err)
	}
	fp.deleted = nil
	fp.machines["default"] = fp.machines["default"][:1]
	r.runOnce(context.Background())
	if len(fp.deleted) != 1 || fp.deleted[0] != "default/keep" {
		t.Fatalf("after unprotect deleted %v", fp.deleted)
	}
}
