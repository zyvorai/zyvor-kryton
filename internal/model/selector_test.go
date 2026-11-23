// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package model

import (
	"fmt"
	"strings"
	"testing"
)

func TestSelectorMatches(t *testing.T) {
	labels := map[string]string{"env": "ci", "team": "core", "tier": "web"}
	cases := []struct {
		sel  string
		want bool
	}{
		{"", true},
		{"env=ci", true},
		{"env==ci", true},
		{"env=prod", false},
		{"env!=prod", true},
		{"env!=ci", false},
		{"missing!=x", true},
		{"env", true},
		{"missing", false},
		{"!missing", true},
		{"!env", false},
		{"env in (ci,prod)", true},
		{"env in (prod, qa)", false},
		{"env notin (prod,qa)", true},
		{"env notin (ci)", false},
		{"missing notin (a)", true},
		{"env=ci,team=core", true},
		{"env=ci,team=other", false},
		{"env in (ci,prod),tier!=db,!gone", true},
		{" env = ci , team = core ", true},
	}
	for _, c := range cases {
		sel, err := ParseSelector(c.sel)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.sel, err)
			continue
		}
		if got := sel.Matches(labels); got != c.want {
			t.Errorf("%q: got %v want %v", c.sel, got, c.want)
		}
	}
	if !(Selector(nil)).Matches(nil) {
		t.Error("empty selector must match a machine without labels")
	}
}

func TestParseSelectorRejects(t *testing.T) {
	many := strings.Repeat("a=b,", MaxSelectorRequirements) + "c=d"
	var vals []string
	for i := 0; i <= MaxSelectorValues; i++ {
		vals = append(vals, fmt.Sprintf("v%d", i))
	}
	bad := map[string]string{
		"empty requirement": "a=b,,c=d",
		"trailing comma":    "a=b,",
		"bad key chars":     "a b=c",
		"bad value chars":   "a=b$c",
		"key with quote":    `a"=b`,
		"empty key":         "=b",
		"empty not-exists":  "!",
		"unbalanced open":   "a in (b,c",
		"unbalanced close":  "a=b)",
		"nested parens":     "a in ((b))",
		"unknown set op":    "a between (b,c)",
		"set without key":   "in (a,b)",
		"paren in equality": "a=(b)",
		"too long":          "a=" + strings.Repeat("b", MaxSelectorBytes),
		"too many reqs":     many,
		"too many set vals": "a in (" + strings.Join(vals, ",") + ")",
		"empty set value":   "a in ()",
		"nul byte":          "a=b\x00",
		"newline in key":    "a\n=b",
		"huge key":          strings.Repeat("k", 200) + "=v",
		"value too long":    "a=" + strings.Repeat("v", 300),
	}
	for name, s := range bad {
		if _, err := ParseSelector(s); err == nil {
			t.Errorf("%s: %q was accepted", name, s)
		}
	}
}

func TestValidateMachineSpecCapsLabels(t *testing.T) {
	spec := MachineSpec{Name: "a", Image: "x", Compute: ComputeSpec{CPU: 1, MemoryMiB: 1024}, Disk: DiskSpec{SizeGiB: 20}, Labels: map[string]string{}}
	for i := 0; i < MaxLabels; i++ {
		spec.Labels[fmt.Sprintf("k%d", i)] = "v"
	}
	if err := ValidateMachineSpec(spec); err != nil {
		t.Fatalf("%d labels should be accepted: %v", MaxLabels, err)
	}
	spec.Labels["one-too-many"] = "v"
	if err := ValidateMachineSpec(spec); err == nil || !strings.Contains(err.Error(), "labels") {
		t.Fatalf("expected label cap error, got %v", err)
	}
}
