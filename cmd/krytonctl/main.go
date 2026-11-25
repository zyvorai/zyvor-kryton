// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

// Command krytonctl is the Kryton CLI: a thin HTTP client over the krytond
// REST API for scripting and interactive use (list/create/get/start/stop/
// delete, snapshots, doctor, images, capabilities, events, and API-key
// token generation), configured via KRYTON_URL, KRYTON_TOKEN, and
// KRYTON_PROJECT.
package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/model"
)

type client struct {
	base, token, project string
	idemKey              string // sent as Idempotency-Key when set
	http                 *http.Client
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	if os.Args[1] == "hash-token" {
		if len(os.Args) != 3 {
			fmt.Fprintln(os.Stderr, "usage: krytonctl hash-token TOKEN")
			os.Exit(2)
		}
		fmt.Println(auth.HashToken(os.Args[2]))
		return
	}
	if os.Args[1] == "new-key" {
		newKeyCommand(os.Args[2:])
		return
	}
	if os.Args[1] == "generate-token" {
		var b [32]byte
		if _, err := rand.Read(b[:]); err != nil {
			panic(err)
		}
		fmt.Println(base64.RawURLEncoding.EncodeToString(b[:]))
		return
	}
	c := client{base: strings.TrimRight(env("KRYTON_URL", "http://localhost:8080"), "/"), token: os.Getenv("KRYTON_TOKEN"), project: env("KRYTON_PROJECT", "default"), http: &http.Client{Timeout: 30 * time.Second}}
	switch os.Args[1] {
	case "list":
		listCommand(c, os.Args[2:])
	case "protect", "unprotect":
		protectCommand(c, os.Args[1], os.Args[2:])
	case "snapshot-report":
		snapshotReportCommand(c, os.Args[2:])
	case "bulk":
		bulkCommand(c, os.Args[2:])
	case "summary":
		summaryCommand(c, os.Args[2:])
	case "images":
		c.do("GET", "/api/v1/images", nil)
	case "events":
		eventsCommand(c, os.Args[2:])
	case "capabilities":
		c.do("GET", "/api/v1/capabilities", nil)
	case "doctor":
		c.do("GET", "/api/v1/doctor", nil)
	case "storage":
		c.do("GET", "/api/v1/storage", nil)
	case "set-storage":
		if len(os.Args) < 3 {
			fmt.Fprintln(os.Stderr, "set-storage requires STORAGE_CLASS (or \"\" to clear)")
			os.Exit(2)
		}
		c.do("PUT", "/api/v1/storage/config", map[string]any{"storageClass": os.Args[2]})
	case "metrics":
		if t := os.Getenv("KRYTON_METRICS_TOKEN"); t != "" {
			c.token = t
		}
		c.do("GET", "/metrics", nil)
	case "audit":
		auditCommand(c, os.Args[2:])
	case "webhooks":
		webhooksCommand(c, os.Args[2:])
	case "schedules":
		schedulesCommand(c, os.Args[2:])
	case "ttl":
		ttlCommand(c, os.Args[2:])
	case "quota":
		quotaCommand(c, os.Args[2:])
	case "get", "start", "stop", "delete", "snapshot", "snapshots", "restore", "delete-snapshot":
		machineCommand(c, os.Args[1], os.Args[2:])
	case "create":
		createCommand(c, os.Args[2:])
	default:
		if guestCommands[os.Args[1]] {
			guestCommand(c, os.Args[1], os.Args[2:])
			return
		}
		usage()
		os.Exit(2)
	}
}

func createCommand(c client, args []string) {
	f := flag.NewFlagSet("create", flag.ExitOnError)
	image := f.String("image", "windows-server-2025", "image id")
	cpu := f.Int("cpu", 4, "vCPU")
	memory := f.Int("memory", 8192, "memory MiB")
	disk := f.Int("disk", 80, "boot disk GiB")
	sshKey := f.String("ssh-key", "", "SSH public key file for Linux")
	username := f.String("username", "", "Linux username (image default if empty)")
	network := f.String("network", "", "Multus network attachment definition")
	ttl := f.Int("ttl", 0, "TTL in minutes")
	project := f.String("project", c.project, "project")
	var labelFlags stringList
	f.Var(&labelFlags, "label", "machine label key=value (repeatable, at most 64)")
	f.StringVar(&c.idemKey, "idempotency-key", "", "make a retried create safe (1-128 chars A-Za-z0-9_.:-)")
	dUsername := f.String("dockur-username", "", "dockur guest username")
	dPassword := f.String("dockur-password", "", "dockur guest password")
	dHostname := f.String("dockur-hostname", "", "dockur HOST (defaults to machine name)")
	dLanguage := f.String("dockur-language", "", "dockur LANGUAGE")
	dRegion := f.String("dockur-region", "", "dockur REGION")
	dKeyboard := f.String("dockur-keyboard", "", "dockur KEYBOARD")
	dProductKey := f.String("dockur-product-key", "", "dockur product KEY")
	dDomain := f.String("dockur-domain", "", "dockur DOMAIN for AD join")
	dDomainOU := f.String("dockur-domain-ou", "", "dockur DOMAIN_OU")
	dSharedDir := f.String("dockur-shared-dir", "", "legacy: directory name under the machine dir mounted at /shared")
	dOemDir := f.String("dockur-oem-dir", "", "legacy: directory name under the machine dir mounted at /oem")
	dShared := f.Bool("dockur-shared", false, "mount a per-machine shared folder at /shared (guest \\\\host.lan\\Data)")
	dOemScript := f.String("dockur-oem-script", "", "file whose contents become /oem/install.bat (runs at first boot)")
	dJobAgent := f.Bool("dockur-job-agent", false, "install the guest agent so job-run works (implies --dockur-shared)")
	dPublicBind := f.Bool("dockur-public-bind", false, "publish RDP/console on 0.0.0.0 instead of 127.0.0.1")
	dRdpPort := f.Int("dockur-rdp-port", 0, "fixed host RDP port (default auto)")
	dConsolePort := f.Int("dockur-console-port", 0, "fixed host web-viewer port (default auto)")
	dCommand := f.String("dockur-command", "", "post-install COMMAND")
	dCustomISO := f.String("dockur-custom-iso", "", "custom ISO URL or host path")
	dEdition := f.String("dockur-edition", "", "dockur EDITION (e.g. core)")
	dExtraDisks := f.String("dockur-extra-disks", "", "comma-separated extra disk sizes in GiB")
	dAudio := f.Bool("dockur-audio", false, "enable web viewer audio (AUDIO=Y)")
	dSecureBoot := f.Bool("dockur-secure-boot", false, "enable secure boot + TPM")
	dNoAutologin := f.Bool("dockur-no-autologin", false, "disable dockur autologin (AUTOLOGIN=N)")
	parseFlags(f, args, 0)
	if f.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "create requires NAME")
		os.Exit(2)
	}
	body := map[string]any{
		"project": *project, "name": f.Arg(0), "image": *image,
		"compute":    map[string]any{"cpu": *cpu, "memoryMiB": *memory},
		"disk":       map[string]any{"sizeGiB": *disk},
		"network":    map[string]any{"networkId": *network},
		"ttlMinutes": *ttl,
	}
	labels, err := parseLabelFlags(labelFlags)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if labels != nil {
		body["labels"] = labels
	}
	if *sshKey != "" || *username != "" {
		init := model.Initialization{Username: *username}
		if *sshKey != "" {
			data, err := os.ReadFile(*sshKey)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			init.SSHAuthorizedKeys = []string{strings.TrimSpace(string(data))}
		}
		if err := init.Validate(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		body["initialization"] = init
	}
	if dockur := buildDockurOptions(*dUsername, *dPassword, *dHostname, *dLanguage, *dRegion, *dKeyboard, *dProductKey, *dDomain, *dDomainOU, *dSharedDir, *dOemDir, *dCommand, *dCustomISO, *dEdition, *dExtraDisks, *dAudio, *dSecureBoot, *dNoAutologin); dockur != nil {
		body["dockur"] = dockur
	}
	if extra := dockurExtras(*dShared, *dOemScript, *dJobAgent, *dPublicBind, *dRdpPort, *dConsolePort); len(extra) > 0 {
		d, _ := body["dockur"].(map[string]any)
		if d == nil {
			d = map[string]any{}
		}
		for k, v := range extra {
			d[k] = v
		}
		body["dockur"] = d
	}
	c.do("POST", "/api/v1/machines", body)
}

func buildDockurOptions(username, password, hostname, language, region, keyboard, productKey, domain, domainOU, sharedDir, oemDir, command, customISO, edition, extraDisks string, audio, secureBoot, noAutologin bool) map[string]any {
	out := map[string]any{}
	set := func(k, v string) {
		if strings.TrimSpace(v) != "" {
			out[k] = strings.TrimSpace(v)
		}
	}
	set("username", username)
	set("password", password)
	set("hostname", hostname)
	set("language", language)
	set("region", region)
	set("keyboard", keyboard)
	set("productKey", productKey)
	set("domain", domain)
	set("domainOu", domainOU)
	set("sharedDir", sharedDir)
	set("oemDir", oemDir)
	set("command", command)
	set("customIso", customISO)
	set("edition", edition)
	if extraDisks != "" {
		var sizes []int
		for _, part := range strings.Split(extraDisks, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			n, err := strconv.Atoi(part)
			if err != nil || n <= 0 {
				fmt.Fprintf(os.Stderr, "invalid dockur-extra-disks entry %q\n", part)
				os.Exit(2)
			}
			sizes = append(sizes, n)
		}
		if len(sizes) > 0 {
			out["extraDisksGiB"] = sizes
		}
	}
	if audio {
		out["audio"] = true
	}
	if secureBoot {
		out["secureBoot"] = true
	}
	if noAutologin {
		out["autologin"] = false
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
func machineCommand(c client, cmd string, args []string) {
	f := flag.NewFlagSet(cmd, flag.ExitOnError)
	project := f.String("project", c.project, "project")
	name := f.String("name", "", "snapshot name")
	parseFlags(f, args, 0)
	if f.NArg() < 1 {
		fmt.Fprintf(os.Stderr, "%s requires MACHINE_ID\n", cmd)
		os.Exit(2)
	}
	id := f.Arg(0)
	q := "?project=" + *project
	switch cmd {
	case "get":
		c.do("GET", "/api/v1/machines/"+id+q, nil)
	case "start", "stop":
		c.do("POST", "/api/v1/machines/"+id+"/"+cmd+q, nil)
	case "delete":
		c.do("DELETE", "/api/v1/machines/"+id+q, nil)
	case "snapshot":
		c.do("POST", "/api/v1/machines/"+id+"/snapshot"+q, map[string]any{"name": *name})
	case "snapshots":
		c.do("GET", "/api/v1/machines/"+id+"/snapshots"+q, nil)
	case "restore":
		if f.NArg() != 2 {
			fmt.Fprintln(os.Stderr, "restore requires MACHINE_ID SNAPSHOT_ID")
			os.Exit(2)
		}
		c.do("POST", "/api/v1/machines/"+id+"/snapshots/"+f.Arg(1)+"/restore"+q, nil)
	case "delete-snapshot":
		if f.NArg() != 2 {
			fmt.Fprintln(os.Stderr, "delete-snapshot requires MACHINE_ID SNAPSHOT_ID")
			os.Exit(2)
		}
		c.do("DELETE", "/api/v1/machines/"+id+"/snapshots/"+f.Arg(1)+q, nil)
	}
}
func (c client) do(method, path string, body any) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	// #nosec G704 -- the CLI talks to the Kryton URL its operator configured
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.idemKey != "" {
		req.Header.Set("Idempotency-Key", c.idemKey)
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
	b, _ := io.ReadAll(res.Body)
	var pretty bytes.Buffer
	if json.Indent(&pretty, b, "", "  ") == nil {
		fmt.Println(pretty.String())
	} else {
		fmt.Print(string(b))
	}
	if res.StatusCode >= 400 {
		os.Exit(1)
	}
}
func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func fatal(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
func usage() {
	fmt.Println(`krytonctl commands:
  list [--selector 'env=ci,tier!=db'] [--state running,stopped] [--name SUBSTR]
  summary [--group-by LABEL_KEY]                   machine totals, optionally per label value
  create NAME [--image ID --cpu N --memory MiB --disk GiB --network NAD --ttl MIN --label k=v ... --idempotency-key KEY]
    dockur flags (provider=dockur):
      --dockur-username --dockur-password --dockur-hostname
      --dockur-language --dockur-region --dockur-keyboard
      --dockur-product-key --dockur-domain --dockur-domain-ou
      --dockur-shared --dockur-oem-script FILE --dockur-job-agent
      --dockur-public-bind --dockur-rdp-port N --dockur-console-port N
      --dockur-shared-dir --dockur-oem-dir (legacy names under the machine dir)
      --dockur-command
      --dockur-custom-iso --dockur-edition --dockur-extra-disks 32,64
      --dockur-audio --dockur-secure-boot --dockur-no-autologin
    A generated password is printed once when --dockur-password is omitted.
  ready MACHINE_ID [--wait --timeout SEC]          exit 1 until the guest is ready
  restart MACHINE_ID [--force --no-wait --timeout SEC]
  logs MACHINE_ID [--tail N]
  shared-ls MACHINE_ID [DIR]
  shared-put MACHINE_ID LOCAL REMOTE [--overwrite]
  shared-get MACHINE_ID REMOTE [-o FILE]
  shared-rm MACHINE_ID REMOTE
  job-run MACHINE_ID [--shell cmd|powershell --timeout SEC --name N --env K=V --wait --file F] [COMMAND...]
  jobs MACHINE_ID
  job-get|job-cancel|job-logs|job-artifacts MACHINE_ID JOB_ID
  job-artifact MACHINE_ID JOB_ID PATH [-o FILE]
  get MACHINE_ID
  start MACHINE_ID
  stop MACHINE_ID
  bulk start|stop|snapshot|delete (--selector EXPR | MACHINE_ID...) [--dry-run] [--confirm] [--snapshot-name N]
                                                   up to 100 machines, per-machine results; delete needs --confirm or --dry-run
  protect MACHINE_ID [--reason TEXT]               block delete, bulk delete and TTL expiry
  unprotect MACHINE_ID
  snapshot-report [--older-than 30d] [--selector EXPR] [--limit N]   snapshots across the project, oldest first
  events [--type T[,T]] [--machine ID] [--since RFC3339] [--limit N]
  snapshot MACHINE_ID [--name NAME]
  snapshots MACHINE_ID
  restore MACHINE_ID SNAPSHOT_ID
  delete-snapshot MACHINE_ID SNAPSHOT_ID
  delete MACHINE_ID
  ttl MACHINE_ID MINUTES [--project P]      (0 = never expire)
  schedules list [--machine ID]
  schedules create MACHINE_ID --action start|stop|restart|snapshot --cron "0 19 * * mon-fri" [--tz Europe/Berlin] [--snapshot-name N]
                   [--retain N] [--max-failures N]
  schedules update SCHEDULE_ID [--cron C --tz Z --action A --snapshot-name N --retain N --max-failures N]
  schedules get|run|enable|disable|delete SCHEDULE_ID
  quota list | get PROJECT | clear PROJECT
  quota set PROJECT [--machines N --vcpu N --memory MiB --disk GiB]   (0 = unlimited)
  webhooks list | get ID | delete ID | test ID
  webhooks create URL [--event PATTERN ...] [--description TEXT]
  webhooks update ID [--url U] [--enabled true|false] [--event PATTERN ...]
  webhooks deliveries [--status pending|retrying|succeeded|dead] [--subscription ID]
  webhooks dead-letters | redeliver DELIVERY_ID
  audit [--project P --actor A --action machine.create --machine ID --result success|denied|error
         --since RFC3339 --until RFC3339 --limit N --cursor C]
  metrics                                   (uses KRYTON_METRICS_TOKEN if set, else KRYTON_TOKEN)
  new-key --name N (--role viewer|operator|admin --project p1,p2 | --grant proj=role ...)
  images
  events
  capabilities
  doctor
  storage
  set-storage STORAGE_CLASS
  generate-token
  hash-token TOKEN

Environment: KRYTON_URL, KRYTON_TOKEN, KRYTON_PROJECT, KRYTON_METRICS_TOKEN`)
}
