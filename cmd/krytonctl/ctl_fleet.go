// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package main

import (
	"flag"
	"fmt"
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
	_ = f.Parse(reorderFlags(args))
	c.do("GET", "/api/v1/machines"+query("project", *project, "selector", *selector), nil)
}

func bulkCommand(c client, args []string) {
	f := flag.NewFlagSet("bulk", flag.ExitOnError)
	project := f.String("project", c.project, "project")
	selector := f.String("selector", "", "label selector choosing the machines")
	_ = f.Parse(reorderFlags(args))
	if f.NArg() < 1 || (f.Arg(0) != "start" && f.Arg(0) != "stop") {
		usageExit("bulk requires start|stop and either --selector or MACHINE_ID...")
	}
	ids := f.Args()[1:]
	if (len(ids) == 0) == (*selector == "") {
		usageExit("bulk needs exactly one of --selector or MACHINE_ID...")
	}
	body := map[string]any{"project": *project, "action": f.Arg(0)}
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
	_ = f.Parse(reorderFlags(args))
	c.do("GET", "/api/v1/events"+query("limit", *limit, "type", *typ, "machineId", *machine, "since", *since), nil)
}
