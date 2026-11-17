// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// guestCommands are the dockur guest subcommands (readiness, restart,
// shared folder, jobs).
var guestCommands = map[string]bool{
	"ready": true, "restart": true, "logs": true,
	"shared-ls": true, "shared-put": true, "shared-get": true, "shared-rm": true,
	"job-run": true, "jobs": true, "job-get": true, "job-logs": true, "job-cancel": true,
	"job-artifacts": true, "job-artifact": true,
}

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

// escapePath percent-escapes each segment of a slash-separated path.
func escapePath(p string) string {
	segs := strings.Split(strings.Trim(p, "/"), "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// raw performs a request with a non-JSON body/response and returns the
// status code; a response body is copied to out when non-nil, otherwise
// printed.
func (c client) raw(method, path string, body io.Reader, size int64, out io.Writer) int {
	// #nosec G704 -- the CLI talks to the Kryton URL its operator configured
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/octet-stream")
		req.ContentLength = size
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	hc := &http.Client{} // transfers can outlast the 30s API timeout
	// #nosec G704 -- request built above for the operator-configured Kryton URL
	res, err := hc.Do(req)
	if err != nil {
		fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode >= 400 || out == nil {
		b, _ := io.ReadAll(res.Body)
		var pretty bytes.Buffer
		if json.Indent(&pretty, b, "", "  ") == nil {
			fmt.Println(pretty.String())
		} else {
			fmt.Print(string(b))
		}
		return res.StatusCode
	}
	if _, err := io.Copy(out, res.Body); err != nil {
		fatal(err)
	}
	return res.StatusCode
}

func (c client) getJSON(path string, v any) int {
	// #nosec G704 -- operator-configured Kryton URL
	req, err := http.NewRequest("GET", c.base+path, nil)
	if err != nil {
		fatal(err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	// #nosec G704 -- request built above for the operator-configured Kryton URL
	res, err := c.http.Do(req)
	if err != nil {
		fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	_ = json.NewDecoder(res.Body).Decode(v)
	return res.StatusCode
}

func guestCommand(c client, cmd string, args []string) {
	f := flag.NewFlagSet(cmd, flag.ExitOnError)
	project := f.String("project", c.project, "project")
	wait := f.Bool("wait", false, "ready/job-run: wait for the guest / job to finish")
	timeout := f.Int("timeout", 0, "seconds (ready --wait, restart, job timeout)")
	force := f.Bool("force", false, "restart: kill instead of ACPI shutdown")
	noWait := f.Bool("no-wait", false, "restart: do not wait for RDP")
	tail := f.Int("tail", 200, "logs: lines")
	overwrite := f.Bool("overwrite", false, "shared-put: replace an existing file")
	outFile := f.String("o", "", "shared-get/job-artifact: output file (default stdout)")
	shell := f.String("shell", "cmd", "job-run: cmd or powershell")
	name := f.String("name", "", "job-run: job name")
	file := f.String("file", "", "job-run: read the script from FILE")
	var envs multiFlag
	f.Var(&envs, "env", "job-run: KEY=VALUE (repeatable)")
	_ = f.Parse(args)
	if f.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "%s requires MACHINE_ID\n", cmd)
		os.Exit(2)
	}
	id := f.Arg(0)
	q := "?project=" + url.QueryEscape(*project)
	base := "/api/v1/machines/" + id
	need := func(n int, usage string) {
		if f.NArg() != n {
			fmt.Fprintln(os.Stderr, "usage: krytonctl "+cmd+" "+usage)
			os.Exit(2)
		}
	}
	dest := func() io.Writer {
		if *outFile == "" {
			return os.Stdout
		}
		o, err := os.Create(*outFile)
		if err != nil {
			fatal(err)
		}
		return o
	}
	switch cmd {
	case "ready":
		path := base + "/ready" + q
		if *wait {
			t := *timeout
			if t <= 0 {
				t = 120
			}
			path += "&wait=true&timeoutSeconds=" + strconv.Itoa(t)
			c.http = &http.Client{Timeout: time.Duration(t+30) * time.Second}
		}
		c.do("GET", path, nil) // exits 1 on 503, so scripts can gate on it
	case "restart":
		body := map[string]any{"force": *force, "wait": !*noWait}
		if *timeout > 0 {
			body["timeoutSeconds"] = *timeout
			c.http = &http.Client{Timeout: time.Duration(*timeout+60) * time.Second}
		} else {
			c.http = &http.Client{Timeout: 35 * time.Minute}
		}
		c.do("POST", base+"/restart"+q, body)
	case "logs":
		if c.raw("GET", base+"/logs"+q+"&tail="+strconv.Itoa(*tail), nil, 0, os.Stdout) >= 400 {
			os.Exit(1)
		}
	case "shared-ls":
		p := ""
		if f.NArg() > 1 {
			p = "&path=" + url.QueryEscape(f.Arg(1))
		}
		c.do("GET", base+"/shared"+q+p, nil)
	case "shared-put":
		need(3, "MACHINE_ID LOCAL_FILE REMOTE_PATH [--overwrite]")
		fh, err := os.Open(f.Arg(1))
		if err != nil {
			fatal(err)
		}
		defer func() { _ = fh.Close() }()
		st, err := fh.Stat()
		if err != nil {
			fatal(err)
		}
		path := base + "/shared/" + escapePath(f.Arg(2)) + q
		if *overwrite {
			path += "&overwrite=true"
		}
		if c.raw("PUT", path, fh, st.Size(), nil) >= 400 {
			os.Exit(1)
		}
	case "shared-get":
		need(2, "MACHINE_ID REMOTE_PATH [-o FILE]")
		if c.raw("GET", base+"/shared/"+escapePath(f.Arg(1))+q, nil, 0, dest()) >= 400 {
			os.Exit(1)
		}
	case "shared-rm":
		need(2, "MACHINE_ID REMOTE_PATH")
		c.do("DELETE", base+"/shared/"+escapePath(f.Arg(1))+q, nil)
	case "jobs":
		c.do("GET", base+"/jobs"+q, nil)
	case "job-get":
		need(2, "MACHINE_ID JOB_ID")
		c.do("GET", base+"/jobs/"+f.Arg(1)+q, nil)
	case "job-cancel":
		need(2, "MACHINE_ID JOB_ID")
		c.do("POST", base+"/jobs/"+f.Arg(1)+"/cancel"+q, nil)
	case "job-logs":
		need(2, "MACHINE_ID JOB_ID")
		if c.raw("GET", base+"/jobs/"+f.Arg(1)+"/logs"+q, nil, 0, os.Stdout) >= 400 {
			os.Exit(1)
		}
	case "job-artifacts":
		need(2, "MACHINE_ID JOB_ID")
		c.do("GET", base+"/jobs/"+f.Arg(1)+"/artifacts"+q, nil)
	case "job-artifact":
		need(3, "MACHINE_ID JOB_ID PATH [-o FILE]")
		if c.raw("GET", base+"/jobs/"+f.Arg(1)+"/artifacts/"+escapePath(f.Arg(2))+q, nil, 0, dest()) >= 400 {
			os.Exit(1)
		}
	case "job-run":
		script := strings.Join(f.Args()[1:], " ")
		if *file != "" {
			b, err := os.ReadFile(*file)
			if err != nil {
				fatal(err)
			}
			script = string(b)
		}
		if strings.TrimSpace(script) == "" {
			fmt.Fprintln(os.Stderr, "usage: krytonctl job-run MACHINE_ID [--shell powershell] [--file F | COMMAND...]")
			os.Exit(2)
		}
		spec := map[string]any{"script": script, "shell": *shell}
		if *name != "" {
			spec["name"] = *name
		}
		if *timeout > 0 {
			spec["timeoutSeconds"] = *timeout
		}
		if len(envs) > 0 {
			m := map[string]string{}
			for _, e := range envs {
				k, v, ok := strings.Cut(e, "=")
				if !ok {
					fmt.Fprintf(os.Stderr, "invalid --env %q (want KEY=VALUE)\n", e)
					os.Exit(2)
				}
				m[k] = v
			}
			spec["env"] = m
		}
		if !*wait {
			c.do("POST", base+"/jobs"+q, spec)
			return
		}
		runAndWait(c, base, q, spec, *timeout)
	}
}

// runAndWait submits a job, polls it to completion, streams its output and
// exits 0 on success, 124 on timeout, 1 otherwise.
func runAndWait(c client, base, q string, spec map[string]any, timeout int) {
	b, _ := json.Marshal(spec)
	req, err := http.NewRequest("POST", c.base+base+"/jobs"+q, bytes.NewReader(b)) // #nosec G704 -- operator-configured URL
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	res, err := c.http.Do(req) // #nosec G704 -- request built above
	if err != nil {
		fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()
	var job struct {
		ID       string `json:"id"`
		State    string `json:"state"`
		ExitCode *int   `json:"exitCode"`
		Message  string `json:"message"`
	}
	if res.StatusCode != http.StatusAccepted || json.Unmarshal(body, &job) != nil {
		fmt.Println(string(body))
		os.Exit(1)
	}
	fmt.Fprintln(os.Stderr, "job", job.ID)
	var offset int64
	for {
		time.Sleep(2 * time.Second)
		var sink bytes.Buffer
		code := c.raw("GET", fmt.Sprintf("%s/jobs/%s/logs%s&offset=%d", base, job.ID, q, offset), nil, 0, &sink)
		if code < 400 {
			offset += int64(sink.Len())
			_, _ = os.Stdout.Write(sink.Bytes())
		}
		if c.getJSON(base+"/jobs/"+job.ID+q, &job) >= 400 {
			os.Exit(1)
		}
		if job.State == "succeeded" || job.State == "failed" || job.State == "timed_out" || job.State == "canceled" {
			break
		}
	}
	var sink bytes.Buffer // final drain
	if c.raw("GET", fmt.Sprintf("%s/jobs/%s/logs%s&offset=%d", base, job.ID, q, offset), nil, 0, &sink) < 400 {
		_, _ = os.Stdout.Write(sink.Bytes())
	}
	fmt.Fprintf(os.Stderr, "job %s: %s %s\n", job.ID, job.State, job.Message)
	switch job.State {
	case "succeeded":
		os.Exit(0)
	case "timed_out":
		os.Exit(124)
	}
	os.Exit(1)
}

// dockurExtras collects the unattended-workflow create options.
func dockurExtras(shared bool, oemScriptFile string, jobAgent, publicBind bool, rdpPort, consolePort int) map[string]any {
	out := map[string]any{}
	if shared {
		out["shared"] = true
	}
	if oemScriptFile != "" {
		b, err := os.ReadFile(oemScriptFile)
		if err != nil {
			fatal(err)
		}
		out["oemScript"] = string(b)
	}
	if jobAgent {
		out["jobAgent"] = true
	}
	if publicBind {
		out["publicBind"] = true
	}
	if rdpPort > 0 {
		out["rdpPort"] = rdpPort
	}
	if consolePort > 0 {
		out["consolePort"] = consolePort
	}
	return out
}
