// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package main

import (
	"flag"
	"fmt"
	"net/url"
	"strings"
)

// parseLabelFlags turns repeated key=value flags into a label map.
func parseLabelFlags(in []string) (map[string]string, error) {
	if len(in) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(in))
	for _, kv := range in {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--label %q must be key=value", kv)
		}
		if _, dup := out[k]; dup {
			return nil, fmt.Errorf("--label %q given twice", k)
		}
		out[k] = v
	}
	return out, nil
}

func listCommand(c client, args []string) {
	f := flag.NewFlagSet("list", flag.ExitOnError)
	project := f.String("project", c.project, "project")
	selector := f.String("selector", "", `label selector, e.g. "env=ci,tier!=db" or "team in (a,b)"`)
	state := f.String("state", "", "comma-separated machine states, e.g. running,stopped")
	name := f.String("name", "", "case-insensitive name substring")
	parseFlags(f, args, 0)
	c.do("GET", "/api/v1/machines"+query("project", *project, "selector", *selector, "state", *state, "name", *name), nil)
}

func bulkCommand(c client, args []string) {
	f := flag.NewFlagSet("bulk", flag.ExitOnError)
	project := f.String("project", c.project, "project")
	selector := f.String("selector", "", "label selector choosing the machines")
	dryRun := f.Bool("dry-run", false, "validate every target and change nothing")
	confirm := f.Bool("confirm", false, "required for a real bulk delete")
	snapName := f.String("snapshot-name", "", "name for the snapshots (snapshot action)")
	parseFlags(f, args, 0)
	switch f.Arg(0) {
	case "start", "stop", "snapshot", "delete":
	default:
		usageExit("bulk requires start|stop|snapshot|delete and either --selector or MACHINE_ID...")
	}
	ids := f.Args()[1:]
	if (len(ids) == 0) == (*selector == "") {
		usageExit("bulk needs exactly one of --selector or MACHINE_ID...")
	}
	body := map[string]any{"project": *project, "action": f.Arg(0)}
	if *dryRun {
		body["dryRun"] = true
	}
	if *confirm {
		body["confirm"] = true
	}
	if *snapName != "" {
		body["snapshotName"] = *snapName
	}
	if *selector != "" {
		body["selector"] = *selector
	} else {
		body["ids"] = ids
	}
	c.do("POST", "/api/v1/machines/actions", body)
}

func eventsCommand(c client, args []string) {
	f := flag.NewFlagSet("events", flag.ExitOnError)
	typ := f.String("type", "", "comma-separated event types; trailing * matches a prefix")
	machine := f.String("machine", "", "only events about this machine ID")
	since := f.String("since", "", "only events at or after this RFC 3339 time")
	limit := f.String("limit", "50", "max events")
	parseFlags(f, args, 0)
	c.do("GET", "/api/v1/events"+query("limit", *limit, "type", *typ, "machineId", *machine, "since", *since), nil)
}

func summaryCommand(c client, args []string) {
	f := flag.NewFlagSet("summary", flag.ExitOnError)
	project := f.String("project", c.project, "project")
	groupBy := f.String("group-by", "", "label key to break the totals down by")
	parseFlags(f, args, 0)
	c.do("GET", "/api/v1/summary"+query("project", *project, "groupBy", *groupBy), nil)
}

// protectCommand sets or removes delete protection on a machine.
func protectCommand(c client, cmd string, args []string) {
	f := flag.NewFlagSet(cmd, flag.ExitOnError)
	project := f.String("project", c.project, "project")
	reason := f.String("reason", "", "why the machine is protected")
	parseFlags(f, args, 0)
	if f.NArg() != 1 || (cmd == "unprotect" && *reason != "") {
		usageExit("usage: protect MACHINE_ID [--reason TEXT] | unprotect MACHINE_ID")
	}
	body := map[string]any{"protected": cmd == "protect"}
	if *reason != "" {
		body["reason"] = *reason
	}
	c.do("PUT", "/api/v1/machines/"+url.PathEscape(f.Arg(0))+"/protection"+query("project", *project), body)
}

// snapshotReportCommand lists snapshots across a project, oldest first.
func snapshotReportCommand(c client, args []string) {
	f := flag.NewFlagSet("snapshot-report", flag.ExitOnError)
	project := f.String("project", c.project, "project")
	olderThan := f.String("older-than", "", `only snapshots older than this, e.g. "36h" or "30d"`)
	selector := f.String("selector", "", "label selector choosing the machines")
	limit := f.String("limit", "", "max snapshots (default 100, at most 500)")
	parseFlags(f, args, 0)
	if f.NArg() != 0 {
		usageExit("snapshot-report takes only flags")
	}
	c.do("GET", "/api/v1/snapshots"+query("project", *project, "olderThan", *olderThan, "selector", *selector, "limit", *limit), nil)
}
