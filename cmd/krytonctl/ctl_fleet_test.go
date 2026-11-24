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
