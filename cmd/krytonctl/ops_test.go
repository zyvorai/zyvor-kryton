// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package main

import (
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

type seen struct {
	Method, Path, RawQuery, Auth string
	Body                         map[string]any
}

func capture(t *testing.T) (client, *[]seen) {
	t.Helper()
	var got []seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		s := seen{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Auth: r.Header.Get("Authorization")}
		if len(b) > 0 {
			_ = json.Unmarshal(b, &s.Body)
		}
		got = append(got, s)
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(srv.Close)
	return client{base: srv.URL, token: "tok", project: "lab", http: srv.Client()}, &got
}

func TestQueryAndReorder(t *testing.T) {
	if q := query("a", "1", "b", "", "c", "x y"); q != "?a=1&c=x+y" {
		t.Fatalf("query=%q", q)
	}
	if query("a", "") != "" {
		t.Fatal("empty query should be empty string")
	}
}

func TestParseFlags(t *testing.T) {
	cases := []struct {
		args     []string
		verbatim int
		want     []string // positionals
		project  string
		force    bool
		wait     bool
	}{
		{args: []string{"id", "--project", "ops", "--force"}, want: []string{"id"}, project: "ops", force: true},
		{args: []string{"--force", "id", "--project=ops"}, want: []string{"id"}, project: "ops", force: true},
		// A boolean flag must not swallow the next flag or argument.
		{args: []string{"delete", "--force", "--project", "ops", "m1"}, want: []string{"delete", "m1"}, project: "ops", force: true},
		{args: []string{"--force", "m1", "--wait"}, want: []string{"m1"}, force: true, wait: true},
		{args: []string{"id", "--", "--project", "x"}, want: []string{"id", "--project", "x"}},
		// job-run: flags before the command are ours, the rest is the guest's.
		{args: []string{"id", "--wait", "ping", "--project", "x", "-n", "3"}, verbatim: 2, want: []string{"id", "ping", "--project", "x", "-n", "3"}, wait: true},
		{args: []string{"id", "--", "-weird"}, verbatim: 2, want: []string{"id", "-weird"}},
	}
	for _, c := range cases {
		f := flag.NewFlagSet("t", flag.ContinueOnError)
		project := f.String("project", "", "")
		force := f.Bool("force", false, "")
		wait := f.Bool("wait", false, "")
		parseFlags(f, c.args, c.verbatim)
		if !reflect.DeepEqual(f.Args(), c.want) || *project != c.project || *force != c.force || *wait != c.wait {
			t.Errorf("%v: args=%q project=%q force=%v wait=%v", c.args, f.Args(), *project, *force, *wait)
		}
	}
}

// Documented forms: the positional comes first and flags follow it.
func TestFlagsAfterPositionalsReachTheServer(t *testing.T) {
	c, got := capture(t)
	createCommand(c, []string{"vm1", "--cpu", "2", "--label", "env=ci", "--project", "ops"})
	machineCommand(c, "delete", []string{"m1", "--project", "ops"})
	guestCommand(c, "restart", []string{"m1", "--force", "--no-wait", "--project", "ops"})
	bulkCommand(c, []string{"delete", "--dry-run", "--selector", "env=tmp"})
	if len(*got) != 4 {
		t.Fatalf("requests = %+v", *got)
	}
	if g := (*got)[0]; g.Body["name"] != "vm1" || g.Body["project"] != "ops" || g.Body["compute"].(map[string]any)["cpu"] != float64(2) {
		t.Errorf("create = %+v", g)
	}
	if g := (*got)[1]; g.Method != "DELETE" || g.RawQuery != "project=ops" {
		t.Errorf("delete = %+v", g)
	}
	if g := (*got)[2]; g.RawQuery != "project=ops" || g.Body["force"] != true || g.Body["wait"] != false {
		t.Errorf("restart = %+v", g)
	}
	if g := (*got)[3]; g.Body["dryRun"] != true || g.Body["selector"] != "env=tmp" || g.Body["action"] != "delete" {
		t.Errorf("bulk = %+v", g)
	}
}

func TestOpsCommandsHitTheRightEndpoints(t *testing.T) {
	c, got := capture(t)
	webhooksCommand(c, []string{"create", "https://hooks.example/x", "--event", "io.kryton.machine.*", "--description", "ci"})
	webhooksCommand(c, []string{"dead-letters", "--project", "ops"})
	webhooksCommand(c, []string{"redeliver", "d1"})
	webhooksCommand(c, []string{"update", "w1", "--enabled", "false"})
	schedulesCommand(c, []string{"create", "m1", "--action", "stop", "--cron", "0 19 * * mon-fri", "--tz", "Europe/Berlin"})
	schedulesCommand(c, []string{"disable", "s1"})
	ttlCommand(c, []string{"m1", "120"})
	quotaCommand(c, []string{"set", "lab", "--machines", "5", "--vcpu", "16"})
	quotaCommand(c, []string{"clear", "lab"})
	auditCommand(c, []string{"--actor", "alice", "--result", "denied", "--limit", "10"})

	type exp struct{ method, path, rawQuery string }
	want := []exp{
		{"POST", "/api/v1/webhooks", ""},
		{"GET", "/api/v1/webhooks/dead-letters", "limit=50&project=ops"},
		{"POST", "/api/v1/webhooks/deliveries/d1/redeliver", ""},
		{"PUT", "/api/v1/webhooks/w1", ""},
		{"POST", "/api/v1/schedules", ""},
		{"PUT", "/api/v1/schedules/s1", ""},
		{"PUT", "/api/v1/machines/m1/ttl", "project=lab"},
		{"PUT", "/api/v1/quotas/lab", ""},
		{"DELETE", "/api/v1/quotas/lab", ""},
		{"GET", "/api/v1/audit", "actor=alice&limit=10&result=denied"},
	}
	if len(*got) != len(want) {
		t.Fatalf("requests=%d want %d: %+v", len(*got), len(want), *got)
	}
	for i, w := range want {
		g := (*got)[i]
		if g.Method != w.method || g.Path != w.path || g.RawQuery != w.rawQuery || g.Auth != "Bearer tok" {
			t.Errorf("#%d: got %+v want %+v", i, g, w)
		}
	}
	b := (*got)[0].Body
	if b["project"] != "lab" || b["url"] != "https://hooks.example/x" || b["description"] != "ci" || !reflect.DeepEqual(b["eventTypes"], []any{"io.kryton.machine.*"}) {
		t.Fatalf("webhook create body: %v", b)
	}
	if (*got)[3].Body["enabled"] != false {
		t.Fatalf("update body: %v", (*got)[3].Body)
	}
	sb := (*got)[4].Body
	if sb["machineId"] != "m1" || sb["action"] != "stop" || sb["timezone"] != "Europe/Berlin" {
		t.Fatalf("schedule body: %v", sb)
	}
	if (*got)[6].Body["ttlMinutes"].(float64) != 120 {
		t.Fatalf("ttl body: %v", (*got)[6].Body)
	}
	if qb := (*got)[7].Body; qb["maxMachines"].(float64) != 5 || qb["maxVCPU"].(float64) != 16 {
		t.Fatalf("quota body: %v", qb)
	}
}
