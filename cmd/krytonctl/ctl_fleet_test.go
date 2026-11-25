// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package main

import (
	"reflect"
	"testing"
)

func TestParseLabelFlags(t *testing.T) {
	got, err := parseLabelFlags([]string{"env=ci", "note=a=b", "empty="})
	if err != nil || !reflect.DeepEqual(got, map[string]string{"env": "ci", "note": "a=b", "empty": ""}) {
		t.Fatalf("got %v %v", got, err)
	}
	if got, err := parseLabelFlags(nil); got != nil || err != nil {
		t.Fatalf("nil input: %v %v", got, err)
	}
	for _, bad := range [][]string{{"novalue"}, {"=v"}, {"a=1", "a=2"}} {
		if _, err := parseLabelFlags(bad); err == nil {
			t.Errorf("%v was accepted", bad)
		}
	}
}

func TestFleetCommandsHitTheRightEndpoints(t *testing.T) {
	c, got := capture(t)
	listCommand(c, []string{"--selector", "env in (ci,prod),!old"})
	bulkCommand(c, []string{"stop", "--selector", "env=ci"})
	bulkCommand(c, []string{"start", "m1", "m2", "--project", "ops"})
	eventsCommand(c, []string{"--type", "io.kryton.machine.*", "--machine", "m1"})

	type exp struct{ method, path, rawQuery string }
	want := []exp{
		{"GET", "/api/v1/machines", "project=lab&selector=env+in+%28ci%2Cprod%29%2C%21old"},
		{"POST", "/api/v1/machines/actions", ""},
		{"POST", "/api/v1/machines/actions", ""},
		{"GET", "/api/v1/events", "limit=50&machineId=m1&type=io.kryton.machine.%2A"},
	}
	if len(*got) != len(want) {
		t.Fatalf("requests=%d want %d: %+v", len(*got), len(want), *got)
	}
	for i, w := range want {
		g := (*got)[i]
		if g.Method != w.method || g.Path != w.path || g.RawQuery != w.rawQuery {
			t.Errorf("request %d = %s %s ?%s, want %s %s ?%s", i, g.Method, g.Path, g.RawQuery, w.method, w.path, w.rawQuery)
		}
	}
	if b := (*got)[1].Body; b["action"] != "stop" || b["selector"] != "env=ci" || b["project"] != "lab" {
		t.Errorf("bulk selector body = %v", b)
	}
	if b := (*got)[2].Body; b["project"] != "ops" || !reflect.DeepEqual(b["ids"], []any{"m1", "m2"}) {
		t.Errorf("bulk ids body = %v", b)
	}
}

func TestFleetV2Commands(t *testing.T) {
	c, got := capture(t)
	listCommand(c, []string{"--state", "running,stopped", "--name", "web"})
	summaryCommand(c, []string{"--group-by", "team"})
	bulkCommand(c, []string{"delete", "--selector", "env=tmp", "--dry-run"})
	bulkCommand(c, []string{"delete", "m1", "--confirm"})
	bulkCommand(c, []string{"snapshot", "--selector", "env=ci", "--snapshot-name", "pre"})

	want := []struct{ method, path, rawQuery string }{
		{"GET", "/api/v1/machines", "name=web&project=lab&state=running%2Cstopped"},
		{"GET", "/api/v1/summary", "groupBy=team&project=lab"},
		{"POST", "/api/v1/machines/actions", ""},
		{"POST", "/api/v1/machines/actions", ""},
		{"POST", "/api/v1/machines/actions", ""},
	}
	if len(*got) != len(want) {
		t.Fatalf("requests=%d want %d: %+v", len(*got), len(want), *got)
	}
	for i, w := range want {
		g := (*got)[i]
		if g.Method != w.method || g.Path != w.path || g.RawQuery != w.rawQuery {
			t.Errorf("request %d = %s %s ?%s, want %s %s ?%s", i, g.Method, g.Path, g.RawQuery, w.method, w.path, w.rawQuery)
		}
	}
	if b := (*got)[2].Body; b["action"] != "delete" || b["dryRun"] != true || b["selector"] != "env=tmp" || b["confirm"] != nil {
		t.Errorf("dry-run delete body = %v", b)
	}
	if b := (*got)[3].Body; b["confirm"] != true || b["dryRun"] != nil || !reflect.DeepEqual(b["ids"], []any{"m1"}) {
		t.Errorf("confirmed delete body = %v", b)
	}
	if b := (*got)[4].Body; b["action"] != "snapshot" || b["snapshotName"] != "pre" {
		t.Errorf("snapshot body = %v", b)
	}
}

func TestFleetV3Commands(t *testing.T) {
	c, got := capture(t)
	protectCommand(c, "protect", []string{"m1", "--reason", "prod db"})
	protectCommand(c, "unprotect", []string{"--project", "ops", "m1"})
	snapshotReportCommand(c, []string{"--older-than", "30d", "--selector", "env=ci", "--limit", "10"})
	schedulesCommand(c, []string{"create", "m1", "--action", "snapshot", "--cron", "@daily", "--retain", "7", "--max-failures", "3"})
	schedulesCommand(c, []string{"create", "m1", "--action", "stop", "--cron", "@daily"})
	schedulesCommand(c, []string{"update", "s1", "--retain", "0"})
	schedulesCommand(c, []string{"update", "s1", "--cron", "@hourly"})
	schedulesCommand(c, []string{"run", "s1"})

	want := []struct{ method, path, rawQuery string }{
		{"PUT", "/api/v1/machines/m1/protection", "project=lab"},
		{"PUT", "/api/v1/machines/m1/protection", "project=ops"},
		{"GET", "/api/v1/snapshots", "limit=10&olderThan=30d&project=lab&selector=env%3Dci"},
		{"POST", "/api/v1/schedules", ""},
		{"POST", "/api/v1/schedules", ""},
		{"PUT", "/api/v1/schedules/s1", ""},
		{"PUT", "/api/v1/schedules/s1", ""},
		{"POST", "/api/v1/schedules/s1/run", ""},
	}
	if len(*got) != len(want) {
		t.Fatalf("requests=%d want %d: %+v", len(*got), len(want), *got)
	}
	for i, w := range want {
		g := (*got)[i]
		if g.Method != w.method || g.Path != w.path || g.RawQuery != w.rawQuery {
			t.Errorf("request %d = %s %s ?%s, want %s %s ?%s", i, g.Method, g.Path, g.RawQuery, w.method, w.path, w.rawQuery)
		}
	}
	bodies := []map[string]any{
		0: {"protected": true, "reason": "prod db"},
		1: {"protected": false},
		5: {"retain": float64(0)},
		6: {"cron": "@hourly"},
	}
	for i, w := range bodies {
		if w != nil && !reflect.DeepEqual((*got)[i].Body, w) {
			t.Errorf("body %d = %v, want %v", i, (*got)[i].Body, w)
		}
	}
	if b := (*got)[3].Body; b["retain"] != float64(7) || b["maxFailures"] != float64(3) {
		t.Errorf("create body = %v", b)
	}
	if b := (*got)[4].Body; b["retain"] != nil || b["maxFailures"] != nil {
		t.Errorf("omitted policy flags were sent: %v", b)
	}
}
