// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package reconciler

import (
	"context"
	"testing"
	"time"

	"github.com/zyvorai/kryton/internal/audit"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/schedule"
)

func TestOverridesExtendDisableAndAudit(t *testing.T) {
	fp := &fakeProvider{machines: map[string][]model.Machine{"lab": {
		{ID: "extended", ExpiresAt: expiresAt(-time.Hour)},
		{ID: "forever", ExpiresAt: expiresAt(-time.Hour)},
		{ID: "shortened", ExpiresAt: expiresAt(time.Hour)},
		{ID: "plain", ExpiresAt: expiresAt(-time.Minute)},
	}}}
	st, _ := schedule.NewStore("", nil)
	_, _ = st.SetExpiry("lab", "extended", expiresAt(time.Hour), "alice")
	_, _ = st.SetExpiry("lab", "forever", nil, "alice")
	_, _ = st.SetExpiry("lab", "shortened", expiresAt(-time.Minute), "alice")
	bus, _ := events.New(20, "", "", "", discardLogger())
	au, _ := audit.New(audit.Config{})
	var expired []string
	r := TTL{Provider: fp, Projects: []string{"lab"}, Events: bus, Log: discardLogger(), Overrides: st, Audit: au,
		OnExpire: func(p string) { expired = append(expired, p) }}
	r.runOnce(context.Background())

	got := map[string]bool{}
	for _, d := range fp.deleted {
		got[d] = true
	}
	if !got["lab/plain"] || !got["lab/shortened"] || got["lab/extended"] || got["lab/forever"] || len(got) != 2 {
		t.Fatalf("deleted=%v", fp.deleted)
	}
	if len(expired) != 2 {
		t.Fatalf("OnExpire calls: %v", expired)
	}
	ents, _, _ := au.Query(audit.Filter{Action: "machine.expire"})
	if len(ents) != 2 || ents[0].Actor != "system:ttl" {
		t.Fatalf("audit: %+v", ents)
	}
	if _, ok := st.Expiry("lab", "shortened"); ok {
		t.Fatal("override should be forgotten after deletion")
	}
}

func TestExpiringWarningPublishedOnce(t *testing.T) {
	fp := &fakeProvider{machines: map[string][]model.Machine{"lab": {{ID: "soon", ExpiresAt: expiresAt(10 * time.Minute), Spec: model.MachineSpec{Name: "soon-vm"}}}}}
	bus, _ := events.New(20, "", "", "", discardLogger())
	r := TTL{Provider: fp, Projects: []string{"lab"}, Events: bus, Log: discardLogger(), WarnBefore: 15 * time.Minute, warned: map[string]bool{}}
	r.runOnce(context.Background())
	r.runOnce(context.Background())
	n := 0
	for _, e := range bus.List(0) {
		if e.Type == "io.kryton.machine.expiring" {
			n++
		}
	}
	if n != 1 || len(fp.deleted) != 0 {
		t.Fatalf("expiring events=%d deleted=%v", n, fp.deleted)
	}
}
