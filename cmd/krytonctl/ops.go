// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zyvorai/kryton/internal/auth"
)

// stringList is a repeatable flag.
type stringList []string

func (l *stringList) String() string     { return strings.Join(*l, ",") }
func (l *stringList) Set(v string) error { *l = append(*l, v); return nil }

func usageExit(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(2)
}

func query(kv ...string) string {
	q := url.Values{}
	for i := 0; i+1 < len(kv); i += 2 {
		if kv[i+1] != "" {
			q.Set(kv[i], kv[i+1])
		}
	}
	if len(q) == 0 {
		return ""
	}
	return "?" + q.Encode()
}

func auditCommand(c client, args []string) {
	f := flag.NewFlagSet("audit", flag.ExitOnError)
	project := f.String("project", "", "only this project")
	actor := f.String("actor", "", "API key name / user")
	action := f.String("action", "", "action, e.g. machine.create (trailing * = prefix)")
	machine := f.String("machine", "", "machine id")
	result := f.String("result", "", "success | denied | error")
	since := f.String("since", "", "RFC 3339 lower bound")
	until := f.String("until", "", "RFC 3339 upper bound")
	limit := f.Int("limit", 50, "page size (max 500)")
	cursor := f.String("cursor", "", "cursor from a previous page")
	_ = f.Parse(args)
	c.do("GET", "/api/v1/audit"+query("project", *project, "actor", *actor, "action", *action, "machineId", *machine,
		"result", *result, "since", *since, "until", *until, "limit", strconv.Itoa(*limit), "cursor", *cursor), nil)
}

func webhooksCommand(c client, args []string) {
	if len(args) == 0 {
		usageExit("webhooks requires a subcommand: list | create | get | update | delete | test | deliveries | dead-letters | redeliver")
	}
	sub, rest := args[0], args[1:]
	f := flag.NewFlagSet("webhooks "+sub, flag.ExitOnError)
	project := f.String("project", c.project, "project")
	var events stringList
	f.Var(&events, "event", "event type pattern, repeatable (default: all), e.g. io.kryton.machine.*")
	desc := f.String("description", "", "free-text description")
	status := f.String("status", "", "deliveries: pending | retrying | succeeded | dead")
	subID := f.String("subscription", "", "deliveries: only this subscription id")
	limit := f.Int("limit", 50, "deliveries: page size")
	cursor := f.String("cursor", "", "deliveries: page cursor")
	enabled := f.String("enabled", "", "update: true | false")
	newURL := f.String("url", "", "update: new URL")
	parseFlags(f, rest, 0)
	need := func(n int, what string) {
		if f.NArg() < n {
			usageExit("webhooks " + sub + " requires " + what)
		}
	}
	switch sub {
	case "list":
		c.do("GET", "/api/v1/webhooks"+query("project", *project), nil)
	case "create":
		need(1, "URL")
		c.do("POST", "/api/v1/webhooks", map[string]any{"project": *project, "url": f.Arg(0), "eventTypes": []string(events), "description": *desc})
	case "get":
		need(1, "WEBHOOK_ID")
		c.do("GET", "/api/v1/webhooks/"+f.Arg(0), nil)
	case "update":
		need(1, "WEBHOOK_ID")
		body := map[string]any{}
		if *newURL != "" {
			body["url"] = *newURL
		}
		if *enabled != "" {
			b, err := strconv.ParseBool(*enabled)
			if err != nil {
				usageExit("--enabled must be true or false")
			}
			body["enabled"] = b
		}
		if len(events) > 0 {
			body["eventTypes"] = []string(events)
		}
		if f.Lookup("description").Value.String() != "" {
			body["description"] = *desc
		}
		c.do("PUT", "/api/v1/webhooks/"+f.Arg(0), body)
	case "delete":
		need(1, "WEBHOOK_ID")
		c.do("DELETE", "/api/v1/webhooks/"+f.Arg(0), nil)
	case "test":
		need(1, "WEBHOOK_ID")
		c.do("POST", "/api/v1/webhooks/"+f.Arg(0)+"/test", nil)
	case "deliveries":
		c.do("GET", "/api/v1/webhooks/deliveries"+query("project", *project, "status", *status, "subscriptionId", *subID, "limit", strconv.Itoa(*limit), "cursor", *cursor), nil)
	case "dead-letters":
		c.do("GET", "/api/v1/webhooks/dead-letters"+query("project", *project, "subscriptionId", *subID, "limit", strconv.Itoa(*limit), "cursor", *cursor), nil)
	case "redeliver":
		need(1, "DELIVERY_ID")
		c.do("POST", "/api/v1/webhooks/deliveries/"+f.Arg(0)+"/redeliver", nil)
	default:
		usageExit("unknown webhooks subcommand " + sub)
	}
}

func schedulesCommand(c client, args []string) {
	if len(args) == 0 {
		usageExit("schedules requires a subcommand: list | create | get | update | run | enable | disable | delete")
	}
	sub, rest := args[0], args[1:]
	f := flag.NewFlagSet("schedules "+sub, flag.ExitOnError)
	project := f.String("project", c.project, "project")
	action := f.String("action", "", "start | stop | restart | snapshot")
	cron := f.String("cron", "", `5-field cron, e.g. "0 19 * * mon-fri" or @daily`)
	tz := f.String("tz", "UTC", "IANA timezone, e.g. Europe/Berlin")
	snapName := f.String("snapshot-name", "", "name for scheduled snapshots")
	machine := f.String("machine", "", "list: only this machine")
	retain := f.Int("retain", -1, "snapshot schedules: keep this many snapshots (0 = keep all)")
	maxFailures := f.Int("max-failures", -1, "disable after this many consecutive failures (0 = never)")
	parseFlags(f, rest, 0)
	// policy adds only the flags the caller gave.
	policy := func(body map[string]any) map[string]any {
		if *retain >= 0 {
			body["retain"] = *retain
		}
		if *maxFailures >= 0 {
			body["maxFailures"] = *maxFailures
		}
		return body
	}
	switch sub {
	case "list":
		c.do("GET", "/api/v1/schedules"+query("project", *project, "machineId", *machine), nil)
	case "create":
		if f.NArg() != 1 || *action == "" || *cron == "" {
			usageExit(`schedules create MACHINE_ID --action stop --cron "0 19 * * mon-fri" [--tz Europe/Berlin] [--snapshot-name N] [--retain N] [--max-failures N]`)
		}
		c.do("POST", "/api/v1/schedules", policy(map[string]any{"project": *project, "machineId": f.Arg(0), "action": *action, "cron": *cron, "timezone": *tz, "snapshotName": *snapName}))
	case "update":
		if f.NArg() != 1 {
			usageExit("schedules update requires SCHEDULE_ID")
		}
		body := map[string]any{}
		f.Visit(func(fl *flag.Flag) {
			switch fl.Name {
			case "action":
				body["action"] = *action
			case "cron":
				body["cron"] = *cron
			case "tz":
				body["timezone"] = *tz
			case "snapshot-name":
				body["snapshotName"] = *snapName
			}
		})
		if len(policy(body)) == 0 {
			usageExit("schedules update needs at least one of --action --cron --tz --snapshot-name --retain --max-failures")
		}
		c.do("PUT", "/api/v1/schedules/"+f.Arg(0), body)
	case "get", "delete", "enable", "disable", "run":
		if f.NArg() != 1 {
			usageExit("schedules " + sub + " requires SCHEDULE_ID")
		}
		path := "/api/v1/schedules/" + f.Arg(0)
		switch sub {
		case "get":
			c.do("GET", path, nil)
		case "delete":
			c.do("DELETE", path, nil)
		case "run":
			// A restart or snapshot may take longer than the default timeout;
			// the server gives an action two minutes.
			slow := *c.http
			slow.Timeout = 150 * time.Second
			c.http = &slow
			c.do("POST", path+"/run", nil)
		default:
			c.do("PUT", path, map[string]any{"enabled": sub == "enable"})
		}
	default:
		usageExit("unknown schedules subcommand " + sub)
	}
}

func ttlCommand(c client, args []string) {
	f := flag.NewFlagSet("ttl", flag.ExitOnError)
	project := f.String("project", c.project, "project")
	parseFlags(f, args, 0)
	if f.NArg() != 2 {
		usageExit("ttl requires MACHINE_ID MINUTES (0 = never expire)")
	}
	n, err := strconv.Atoi(f.Arg(1))
	if err != nil || n < 0 {
		usageExit("MINUTES must be a non-negative integer")
	}
	c.do("PUT", "/api/v1/machines/"+f.Arg(0)+"/ttl"+query("project", *project), map[string]any{"ttlMinutes": n})
}

func quotaCommand(c client, args []string) {
	if len(args) == 0 {
		usageExit("quota requires a subcommand: list | get PROJECT | set PROJECT | clear PROJECT")
	}
	sub, rest := args[0], args[1:]
	f := flag.NewFlagSet("quota "+sub, flag.ExitOnError)
	machines := f.Int("machines", 0, "max machines (0 = unlimited)")
	vcpu := f.Int("vcpu", 0, "max total vCPU (0 = unlimited)")
	memory := f.Int("memory", 0, "max total memory MiB (0 = unlimited)")
	disk := f.Int("disk", 0, "max total disk GiB (0 = unlimited)")
	parseFlags(f, rest, 0)
	switch sub {
	case "list":
		c.do("GET", "/api/v1/quotas", nil)
	case "get", "clear", "set":
		if f.NArg() != 1 {
			usageExit("quota " + sub + " requires PROJECT")
		}
		path := "/api/v1/quotas/" + f.Arg(0)
		switch sub {
		case "get":
			c.do("GET", path, nil)
		case "clear":
			c.do("DELETE", path, nil)
		default:
			c.do("PUT", path, map[string]any{"maxMachines": *machines, "maxVCPU": *vcpu, "maxMemoryMiB": *memory, "maxDiskGiB": *disk})
		}
	default:
		usageExit("unknown quota subcommand " + sub)
	}
}

// parseFlags parses args with flags and positional arguments in any order, so
// "create NAME --cpu 2" and "create --cpu 2 NAME" both work (the standard
// flag package stops at the first positional). Boolean flags do not consume
// the next argument. When verbatimAfter > 0, everything from that positional
// onwards is passed through untouched (job-run's guest command). "--" ends
// flag parsing.
func parseFlags(f *flag.FlagSet, args []string, verbatimAfter int) {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			pos = append(pos, args[i+1:]...)
			break
		}
		if len(a) < 2 || a[0] != '-' {
			if verbatimAfter > 0 && len(pos) == verbatimAfter-1 {
				rest := args[i:]
				if len(rest) > 1 && rest[1] == "--" {
					rest = append([]string{rest[0]}, rest[2:]...)
				}
				pos = append(pos, rest...)
				break
			}
			pos = append(pos, a)
			continue
		}
		flags = append(flags, a)
		name, _, inline := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if inline || i+1 >= len(args) {
			continue
		}
		if def := f.Lookup(name); def != nil {
			if b, ok := def.Value.(interface{ IsBoolFlag() bool }); !ok || !b.IsBoolFlag() {
				flags = append(flags, args[i+1])
				i++
			}
		}
	}
	_ = f.Parse(append(append(flags, "--"), pos...))
}

// newKeyCommand prints a fresh token and the keys.json entry for it.
// It runs offline: nothing is sent to the server.
func newKeyCommand(args []string) {
	f := flag.NewFlagSet("new-key", flag.ExitOnError)
	name := f.String("name", "", "key name (appears in the audit log)")
	role := f.String("role", "", "viewer | operator | admin (applies to --project)")
	projects := f.String("project", "", `comma-separated projects for --role, or "*"`)
	var grants stringList
	f.Var(&grants, "grant", "per-project role, repeatable: lab=operator")
	_ = f.Parse(args)
	if *name == "" || (*projects == "" && len(grants) == 0) {
		usageExit("new-key requires --name and --project (with --role) and/or --grant project=role")
	}
	entry := map[string]any{"name": *name}
	if *projects != "" {
		r := auth.Role(*role)
		if r == "" {
			usageExit("--role is required with --project (viewer | operator | admin)")
		}
		entry["role"] = r
		entry["projects"] = splitComma(*projects)
	}
	if len(grants) > 0 {
		g := map[string]string{}
		for _, x := range grants {
			p, r, ok := strings.Cut(x, "=")
			if !ok || p == "" {
				usageExit("--grant must look like project=role")
			}
			g[p] = r
		}
		entry["grants"] = g
	}
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(b[:])
	entry["sha256"] = auth.HashToken(token)
	out, _ := json.MarshalIndent(entry, "", "  ")
	fmt.Fprintf(os.Stderr, "Token (shown once, give it to the key's user):\n")
	fmt.Println(token)
	fmt.Fprintf(os.Stderr, "\nAdd this object to the \"keys\" array in keys.json, then restart krytond:\n")
	fmt.Println(string(out))
}

func splitComma(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
