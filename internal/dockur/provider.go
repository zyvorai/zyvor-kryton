// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

// Package dockur implements provider.Provider on top of dockur/windows:
// it drives Docker or Podman Compose to run real Windows guests in
// containers, mapping Kryton image IDs (e.g. "windows-11-enterprise") to
// dockur VERSION codes (e.g. "11e") and exposing web-viewer/RDP ports per
// machine for lab and development use. Beyond power state it offers a
// first-class shared folder, OEM first-boot script, readiness probing,
// graceful restart and a shared-folder based guest job runner
// (see guest.go, shared.go, jobs.go).
package dockur

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zyvorai/kryton/internal/id"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

// VERSION map aligned with dockur/windows README codes. ARM64 Windows is
// not served by dockur/windows (see dockur/windows-arm) and is rejected.
var defaultVersions = map[string]string{
	"windows-11-enterprise": "11e",
	"windows-11-pro":        "11",
	"windows-11-ltsc":       "11l",
	"windows-10-enterprise": "10e",
	"windows-10-pro":        "10",
	"windows-10-ltsc":       "10l",
	"windows-tiny11":        "tiny11",
	"windows-tiny11-core":   "core11",
	"windows-tiny10":        "tiny10",
	"windows-server-2025":   "2025",
	"windows-server-2022":   "2022",
	"windows-server-2019":   "2019",
	"windows-server-2016":   "2016",
	"windows-server-2012":   "2012",
}

// Runner executes the container runtime (name) with args in dir and
// returns combined stdout+stderr. It is the test seam for a fake docker.
type Runner func(ctx context.Context, dir, name string, args ...string) ([]byte, error)

// Hooks are optional overrides for host interaction, used by tests; nil
// fields use the real implementation.
type Hooks struct {
	Run       Runner
	Dial      func(ctx context.Context, addr string) (net.Conn, error)
	KVMCheck  func() error
	PortFree  func(host string, port int) bool
	PollEvery time.Duration // readiness poll interval (default 3s)
}

// Config configures New; zero-valued fields fall back to defaults
// (docker runtime, ~/.kryton/dockur data dir, 127.0.0.1, port bases
// 18006/13389).
type Config struct {
	Runtime    string // docker (default) or podman
	DataDir    string
	PublicHost string // host used in console URLs and for public-bind RDP
	HTTPBase   int    // first host port for web viewer (default 18006)
	RDPBase    int    // first host port for RDP (default 13389)
	Catalog    VersionResolver
	Hooks      Hooks
}

// VersionResolver maps a Kryton image ID to a dockur/windows VERSION
// code; internal/catalog.Catalog implements it. When unset or when it
// returns false, resolveVersion falls back to defaultVersions.
type VersionResolver interface {
	DockurVersion(imageID string) (string, bool)
}

// Provider implements provider.Provider by driving Docker/Podman Compose
// per machine: each machine gets its own directory under DataDir holding
// a rendered compose.yml, so container/volume state IS the machine's
// state. Machine and port-allocation state is also mirrored to
// state.json (see saveStateLocked/loadState) so a krytond restart
// reattaches to already-running containers. Safe for concurrent use.
type Provider struct {
	mu         sync.Mutex
	jmu        sync.Mutex // serialises guest job file updates
	runtime    string
	dataDir    string
	publicHost string
	httpBase   int
	rdpBase    int
	catalog    VersionResolver
	machines   map[string]map[string]model.Machine // project -> id -> machine
	ports      map[string]portPair                 // machine id -> ports

	run        Runner
	dial       func(ctx context.Context, addr string) (net.Conn, error)
	kvmCheck   func() error
	portFreeFn func(host string, port int) bool
	pollEvery  time.Duration
	// agentFresh is how recent the guest agent heartbeat must be.
	agentFresh time.Duration
	// queueTimeout fails jobs no agent has picked up after this long.
	queueTimeout time.Duration
	sink         provider.EventSink
}

type portPair struct {
	HTTP int `json:"HTTP"`
	RDP  int `json:"RDP"`
	// Bind is the host address the ports are published on. Empty means a
	// machine created before bind control existed (published on all
	// interfaces).
	Bind string `json:"bind,omitempty"`
}

// New builds a dockur Provider, creating cfg.DataDir if needed and
// loading any previously persisted machine/port state from it.
func New(cfg Config) (*Provider, error) {
	runtime := strings.TrimSpace(cfg.Runtime)
	if runtime == "" {
		runtime = "docker"
	}
	dataDir := strings.TrimSpace(cfg.DataDir)
	if dataDir == "" {
		home, _ := os.UserHomeDir()
		dataDir = filepath.Join(home, ".kryton", "dockur")
	}
	if err := os.MkdirAll(dataDir, 0o750); err != nil {
		return nil, err
	}
	httpBase := cfg.HTTPBase
	if httpBase == 0 {
		httpBase = 18006
	}
	rdpBase := cfg.RDPBase
	if rdpBase == 0 {
		rdpBase = 13389
	}
	host := strings.TrimSpace(cfg.PublicHost)
	if host == "" {
		host = "127.0.0.1"
	}
	p := &Provider{
		runtime: runtime, dataDir: dataDir, publicHost: host,
		httpBase: httpBase, rdpBase: rdpBase, catalog: cfg.Catalog,
		machines: map[string]map[string]model.Machine{}, ports: map[string]portPair{},
		run: cfg.Hooks.Run, dial: cfg.Hooks.Dial, kvmCheck: cfg.Hooks.KVMCheck,
		portFreeFn: cfg.Hooks.PortFree, pollEvery: cfg.Hooks.PollEvery,
	}
	p.applyHookDefaults()
	_ = p.loadState()
	return p, nil
}

func (p *Provider) applyHookDefaults() {
	if p.run == nil {
		p.run = func(ctx context.Context, dir, name string, args ...string) ([]byte, error) {
			// #nosec G204 -- configured container runtime; compose file and dir are Kryton-managed
			cmd := exec.CommandContext(ctx, name, args...)
			cmd.Dir = dir
			return cmd.CombinedOutput()
		}
	}
	if p.dial == nil {
		p.dial = func(ctx context.Context, addr string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		}
	}
	if p.kvmCheck == nil {
		p.kvmCheck = func() error { _, err := os.Stat("/dev/kvm"); return err }
	}
	if p.portFreeFn == nil {
		p.portFreeFn = portFree
	}
	if p.pollEvery == 0 {
		p.pollEvery = 3 * time.Second
	}
	if p.agentFresh == 0 {
		p.agentFresh = 90 * time.Second
	}
	if p.queueTimeout == 0 {
		p.queueTimeout = 10 * time.Minute
	}
}

func (p *Provider) Name() string { return "dockur" }

// SetEventSink registers the receiver for provider-originated events
// (guest job submission/completion).
func (p *Provider) SetEventSink(s provider.EventSink) {
	p.jmu.Lock()
	p.sink = s
	p.jmu.Unlock()
}

// ConsoleTarget implements provider.ConsoleResolver: it always returns a
// "web" target proxying the dockur container's noVNC web viewer on its
// allocated HTTP port, since dockur exposes no separate VNC socket.
func (p *Provider) ConsoleTarget(_ context.Context, project, machineID string) (*provider.ConsoleTarget, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.machines[project][machineID]; !ok {
		return nil, provider.ErrNotFound
	}
	ports, ok := p.ports[machineID]
	if !ok {
		return nil, fmt.Errorf("console ports not allocated for machine %s", machineID)
	}
	return &provider.ConsoleTarget{
		Kind:        "web",
		UpstreamURL: fmt.Sprintf("http://127.0.0.1:%d", ports.HTTP),
	}, nil
}

func (p *Provider) consoleURL(project, machineID string) string {
	return fmt.Sprintf("/api/v1/machines/%s/console/?project=%s", machineID, url.QueryEscape(project))
}

func (p *Provider) Capabilities(context.Context) (model.Capabilities, error) {
	return model.Capabilities{Provider: p.Name(), Snapshots: false, Networks: false, TTL: true, Console: true}, nil
}

func (p *Provider) Health(ctx context.Context) error {
	if _, err := exec.LookPath(p.runtime); err != nil {
		return fmt.Errorf("%s not found: %w", p.runtime, err)
	}
	if out, err := p.run(ctx, "", p.runtime, "compose", "version"); err != nil {
		return fmt.Errorf("%s compose unavailable: %v (%s)", p.runtime, err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (p *Provider) Create(ctx context.Context, project string, spec model.MachineSpec) (*model.Machine, error) {
	if isARMImage(spec.Image) {
		return nil, fmt.Errorf("%w: ARM64 Windows is not supported by the dockur provider (dockur/windows is x86_64 only; see dockur/windows-arm)", provider.ErrUnsupported)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.machines[project] == nil {
		p.machines[project] = map[string]model.Machine{}
	}
	for _, existing := range p.machines[project] {
		if existing.Spec.Name == spec.Name {
			return nil, provider.ErrConflict
		}
	}
	version, ok := p.resolveVersion(spec.Image)
	if !ok {
		return nil, fmt.Errorf("%w: image %q has no dockur VERSION mapping", provider.ErrUnsupported, spec.Image)
	}
	if err := p.kvmCheck(); err != nil {
		return nil, fmt.Errorf("dockur requires /dev/kvm (enable VT-x/AMD-V and load kvm modules): %w", err)
	}

	generated := ""
	if spec.Dockur == nil || strings.TrimSpace(spec.Dockur.Password) == "" {
		generated = generatePassword()
	}
	spec = applyDockurDefaults(spec)
	if generated != "" {
		spec.Dockur.Password = generated
	}
	lay, err := layoutFor(spec.Dockur)
	if err != nil {
		return nil, err
	}
	ports, err := p.allocatePortsLocked(spec.Dockur)
	if err != nil {
		return nil, err
	}

	machineID := id.New()
	dir := p.machineDir(project, machineID)
	if err := os.MkdirAll(filepath.Join(dir, "storage"), 0o750); err != nil {
		return nil, err
	}
	if err := prepareDockurDirs(dir, spec.Dockur, lay); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	compose := renderCompose(spec, version, ports)
	if err := os.WriteFile(filepath.Join(dir, "compose.yml"), []byte(compose), 0o600); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if err := p.compose(ctx, dir, "up", "-d", "--pull", "missing"); err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}

	now := time.Now().UTC()
	progress := 10
	m := model.Machine{
		ID: machineID, Project: project, Provider: p.Name(), State: model.StateProvisioning, Spec: spec,
		ProviderRef:     model.ProviderRef{Provider: p.Name(), Namespace: project, Name: spec.Name},
		ConsoleURL:      p.consoleURL(project, machineID),
		RdpUsername:     dockurUsername(spec),
		ProgressPercent: &progress,
		Message:         "Windows image download / unattended install in progress (dockur web viewer)",
		CreatedAt:       now, UpdatedAt: now,
	}
	if spec.TTLMinutes > 0 {
		expires := now.Add(time.Duration(spec.TTLMinutes) * time.Minute)
		m.ExpiresAt = &expires
	}
	p.machines[project][machineID] = m
	p.ports[machineID] = ports
	p.normalizeConsole(&m)
	p.machines[project][machineID] = m
	_ = p.saveStateLocked()
	out := clone(m)
	out.GeneratedPassword = generated
	return out, nil
}

func (p *Provider) Get(ctx context.Context, project, machineID string) (*model.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.machines[project][machineID]
	if !ok {
		return nil, provider.ErrNotFound
	}
	p.refreshLocked(ctx, &m)
	if m.State == model.StateProvisioning || m.State == model.StateStarting {
		if out, err := p.composeOut(ctx, p.machineDir(project, machineID), "logs", "--no-color", "--tail", "200"); err == nil {
			ip := parseInstallProgress(out)
			pct := ip.Percent
			m.ProgressPercent = &pct
			m.Message = "Windows setup: " + ip.Message
		}
	}
	p.machines[project][machineID] = m
	_ = p.saveStateLocked()
	return clone(m), nil
}

func (p *Provider) List(ctx context.Context, project string) ([]model.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []model.Machine
	for id, m := range p.machines[project] {
		p.refreshLocked(ctx, &m)
		p.machines[project][id] = m
		out = append(out, *clone(m))
	}
	_ = p.saveStateLocked()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

func (p *Provider) Start(ctx context.Context, project, machineID string) (*model.Machine, error) {
	return p.lifecycle(ctx, project, machineID, "start", model.StateStarting)
}
func (p *Provider) Stop(ctx context.Context, project, machineID string) (*model.Machine, error) {
	return p.lifecycle(ctx, project, machineID, "stop", model.StateStopping)
}

func (p *Provider) lifecycle(ctx context.Context, project, machineID, action string, transient model.MachineState) (*model.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.machines[project][machineID]
	if !ok {
		return nil, provider.ErrNotFound
	}
	m.State, m.UpdatedAt = transient, time.Now().UTC()
	dir := p.machineDir(project, machineID)
	if err := p.compose(ctx, dir, action); err != nil {
		m.State = model.StateFailed
		m.Message = err.Error()
		p.machines[project][machineID] = m
		_ = p.saveStateLocked()
		return nil, err
	}
	if action == "stop" {
		m.State = model.StateStopped
		m.Message = "Container stopped"
		m.ProgressPercent = nil
	} else {
		// Running is reached once RDP answers (refreshLocked / Ready).
		m.State = model.StateStarting
		m.Message = "Container started; waiting for the guest to answer on RDP"
	}
	m.UpdatedAt = time.Now().UTC()
	p.machines[project][machineID] = m
	_ = p.saveStateLocked()
	return clone(m), nil
}

func (p *Provider) Delete(ctx context.Context, project, machineID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.machines[project][machineID]; !ok {
		return provider.ErrNotFound
	}
	dir := p.machineDir(project, machineID)
	// Tear the guest down on a context detached from the request: a client that
	// gives up during the (slow, graceful) shutdown must not leave a running
	// container behind after the record and its data are gone. If the teardown
	// itself fails, keep the machine so the delete can be retried.
	if _, err := os.Stat(filepath.Join(dir, "compose.yml")); err == nil {
		if err := p.compose(context.WithoutCancel(ctx), dir, "down", "--remove-orphans"); err != nil {
			return fmt.Errorf("dockur: tearing down %s: %w", machineID, err)
		}
	}
	_ = os.RemoveAll(dir)
	delete(p.machines[project], machineID)
	delete(p.ports, machineID)
	return p.saveStateLocked()
}

func (p *Provider) Snapshot(context.Context, string, string, string) (*model.Snapshot, error) {
	return nil, fmt.Errorf("%w: dockur provider does not support snapshots yet", provider.ErrUnsupported)
}
func (p *Provider) ListSnapshots(context.Context, string, string) ([]model.Snapshot, error) {
	return nil, fmt.Errorf("%w: dockur provider does not support snapshots yet", provider.ErrUnsupported)
}
func (p *Provider) RestoreSnapshot(context.Context, string, string, string) (*model.Snapshot, error) {
	return nil, fmt.Errorf("%w: dockur provider does not support snapshots yet", provider.ErrUnsupported)
}
func (p *Provider) DeleteSnapshot(context.Context, string, string, string) error {
	return fmt.Errorf("%w: dockur provider does not support snapshots yet", provider.ErrUnsupported)
}

func isARMImage(imageID string) bool {
	l := strings.ToLower(imageID)
	return strings.Contains(l, "arm64") || strings.Contains(l, "aarch64") || strings.HasSuffix(l, "-arm") || strings.Contains(l, "-arm-")
}

func (p *Provider) resolveVersion(imageID string) (string, bool) {
	if p.catalog != nil {
		if v, ok := p.catalog.DockurVersion(imageID); ok && v != "" {
			return v, true
		}
	}
	v, ok := defaultVersions[imageID]
	return v, ok
}

// allocatePortsLocked reserves the web-viewer and RDP host ports for a
// machine. Explicitly requested ports must be unused by any machine and
// free on the host (provider.ErrConflict otherwise); unrequested ones are
// picked upward from the configured bases. Ports bind to 127.0.0.1
// unless the options opt into a public bind.
func (p *Provider) allocatePortsLocked(d *model.DockurOptions) (portPair, error) {
	bind := "127.0.0.1"
	if d != nil && d.PublicBind {
		bind = "0.0.0.0"
	}
	used := map[int]bool{}
	for _, pp := range p.ports {
		used[pp.HTTP] = true
		used[pp.RDP] = true
	}
	reserve := func(port int, label string) (int, error) {
		if used[port] {
			return 0, fmt.Errorf("%w: %s port %d is already allocated to another machine", provider.ErrConflict, label, port)
		}
		if !p.portFreeFn(bind, port) {
			return 0, fmt.Errorf("%w: %s port %d is already in use on %s", provider.ErrConflict, label, port, bind)
		}
		used[port] = true
		return port, nil
	}
	auto := func(base int, label string) (int, error) {
		for port := base; port <= 65535; port++ {
			if used[port] || !p.portFreeFn(bind, port) {
				continue
			}
			used[port] = true
			return port, nil
		}
		return 0, fmt.Errorf("no free %s port at or above %d", label, base)
	}
	var out portPair
	var err error
	out.Bind = bind
	if d != nil && d.ConsolePort != 0 {
		if out.HTTP, err = reserve(d.ConsolePort, "console"); err != nil {
			return portPair{}, err
		}
	}
	if d != nil && d.RdpPort != 0 {
		if out.RDP, err = reserve(d.RdpPort, "RDP"); err != nil {
			return portPair{}, err
		}
	}
	if out.HTTP == 0 {
		if out.HTTP, err = auto(p.httpBase, "console"); err != nil {
			return portPair{}, err
		}
	}
	if out.RDP == 0 {
		if out.RDP, err = auto(p.rdpBase, "RDP"); err != nil {
			return portPair{}, err
		}
	}
	return out, nil
}

func portFree(host string, port int) bool {
	ln, err := net.Listen("tcp", net.JoinHostPort(host, strconv.Itoa(port)))
	if err != nil {
		return false
	}
	_ = ln.Close()
	return true
}

func (p *Provider) machineDir(project, machineID string) string {
	return filepath.Join(p.dataDir, project, machineID)
}

func (p *Provider) compose(ctx context.Context, dir string, args ...string) error {
	_, err := p.composeOut(ctx, dir, args...)
	return err
}

// composeOut runs `<runtime> compose ... args` for the machine in dir and
// returns its combined output.
func (p *Provider) composeOut(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"compose", "-f", filepath.Join(dir, "compose.yml"), "--project-directory", dir}, args...)
	out, err := p.run(ctx, dir, p.runtime, full...)
	if err != nil {
		return string(out), fmt.Errorf("%s compose %v: %v (%s)", p.runtime, args, err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// refreshLocked updates a machine's state from cheap probes: the guest is
// Running once RDP answers; until then the web viewer reachability only
// moves the message along. Callers hold p.mu.
func (p *Provider) refreshLocked(ctx context.Context, m *model.Machine) {
	p.normalizeConsole(m)
	if m.State == model.StateStopped || m.State == model.StateFailed || m.State == model.StateDeleting || m.State == model.StateStopping {
		return
	}
	ports, ok := p.ports[m.ID]
	if !ok {
		return
	}
	if probeRDP(ctx, p.dial, net.JoinHostPort("127.0.0.1", strconv.Itoa(ports.RDP))) {
		if m.State == model.StateProvisioning || m.State == model.StateStarting {
			m.State = model.StateRunning
			pct := 100
			m.ProgressPercent = &pct
			m.Message = "Guest is answering on RDP"
			m.UpdatedAt = time.Now().UTC()
		}
		return
	}
	if m.State == model.StateProvisioning {
		pct := 35
		m.ProgressPercent = &pct
		m.Message = "Waiting for Windows setup to finish (ISO download / unattended install)"
		m.UpdatedAt = time.Now().UTC()
	}
}

func (p *Provider) normalizeConsole(m *model.Machine) {
	if ports, ok := p.ports[m.ID]; ok && m.Project != "" {
		m.ConsoleURL = p.consoleURL(m.Project, m.ID)
		m.RdpHost = p.publicHost
		if ports.Bind != "" && ports.Bind != "0.0.0.0" {
			m.RdpHost = ports.Bind
		}
		m.RdpPort = ports.RDP
	}
	if m.RdpUsername == "" {
		m.RdpUsername = dockurUsername(m.Spec)
	}
}

const passwordAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// generatePassword returns a 20-character random password with at least
// one lower-case letter, upper-case letter and digit (Windows complexity),
// using only characters that are safe in YAML, cmd and PowerShell.
func generatePassword() string {
	const n = 20
	for {
		b := make([]byte, n)
		for i := range b {
			v, err := rand.Int(rand.Reader, big.NewInt(int64(len(passwordAlphabet))))
			if err != nil {
				panic(fmt.Errorf("secure random source unavailable: %w", err))
			}
			b[i] = passwordAlphabet[v.Int64()]
		}
		s := string(b)
		if strings.ContainsAny(s, "abcdefghijkmnopqrstuvwxyz") && strings.ContainsAny(s, "ABCDEFGHJKLMNPQRSTUVWXYZ") && strings.ContainsAny(s, "23456789") {
			return s
		}
	}
}

func applyDockurDefaults(spec model.MachineSpec) model.MachineSpec {
	d := spec.Dockur
	if d == nil {
		d = &model.DockurOptions{}
	}
	cp := *d
	if strings.TrimSpace(cp.Username) == "" {
		cp.Username = "Docker"
	}
	if strings.TrimSpace(cp.Password) == "" {
		cp.Password = generatePassword()
	}
	if strings.TrimSpace(cp.Hostname) == "" {
		cp.Hostname = spec.Name
	}
	if cp.JobAgent {
		cp.Shared = true
	}
	spec.Dockur = &cp
	return spec
}

func dockurUsername(spec model.MachineSpec) string {
	if spec.Dockur != nil && strings.TrimSpace(spec.Dockur.Username) != "" {
		return spec.Dockur.Username
	}
	return "Docker"
}

// layout names the host sub-directories (relative to the machine dir)
// mounted at /shared and /oem; empty means not mounted.
type layout struct{ Shared, Oem string }

// layoutFor derives the shared/oem directory names from the options and
// re-validates them so no option can point outside the machine directory.
func layoutFor(d *model.DockurOptions) (layout, error) {
	var l layout
	if d == nil {
		return l, nil
	}
	check := func(name, field string) error {
		if err := model.ValidateRelPath(name, false); err != nil {
			return fmt.Errorf("%w: dockur.%s: %v", provider.ErrInvalid, field, err)
		}
		if strings.HasPrefix(name, "storage") || name == "compose.yml" || name == ".." {
			return fmt.Errorf("%w: dockur.%s %q is reserved", provider.ErrInvalid, field, name)
		}
		return nil
	}
	switch {
	case strings.TrimSpace(d.SharedDir) != "":
		l.Shared = strings.TrimSpace(d.SharedDir)
	case d.Shared || d.JobAgent:
		l.Shared = "shared"
	}
	switch {
	case strings.TrimSpace(d.OemDir) != "":
		l.Oem = strings.TrimSpace(d.OemDir)
	case d.OemScript != "" || d.JobAgent:
		l.Oem = "oem"
	}
	if l.Shared != "" {
		if err := check(l.Shared, "sharedDir"); err != nil {
			return l, err
		}
	}
	if l.Oem != "" {
		if err := check(l.Oem, "oemDir"); err != nil {
			return l, err
		}
	}
	if l.Shared != "" && l.Shared == l.Oem {
		return l, fmt.Errorf("%w: sharedDir and oemDir must differ", provider.ErrInvalid)
	}
	return l, nil
}

func layoutOf(m model.Machine) layout {
	l, _ := layoutFor(m.Spec.Dockur)
	return l
}

func prepareDockurDirs(machineDir string, d *model.DockurOptions, lay layout) error {
	if d == nil {
		return nil
	}
	for i := range d.ExtraDisksGiB {
		path := filepath.Join(machineDir, fmt.Sprintf("storage%d", i+2))
		if err := os.MkdirAll(path, 0o750); err != nil {
			return err
		}
	}
	if lay.Shared != "" {
		if err := os.MkdirAll(filepath.Join(machineDir, lay.Shared, ".kryton", "jobs"), 0o755); err != nil { // #nosec G301 -- shared with the guest
			return err
		}
	}
	if lay.Oem != "" {
		oem := filepath.Join(machineDir, lay.Oem)
		if err := os.MkdirAll(oem, 0o755); err != nil { // #nosec G301 -- read by the guest
			return err
		}
		if d.OemScript != "" || d.JobAgent {
			bat := buildInstallBat(d)
			if err := os.WriteFile(filepath.Join(oem, "install.bat"), []byte(bat), 0o644); err != nil { // #nosec G306 -- read by the guest
				return err
			}
		}
		if d.JobAgent {
			if err := os.WriteFile(filepath.Join(oem, "kryton-agent.ps1"), []byte(agentScript), 0o644); err != nil { // #nosec G306 -- read by the guest
				return err
			}
		}
	}
	return nil
}

func yamlQuote(v string) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func renderCompose(spec model.MachineSpec, version string, ports portPair) string {
	ramG := spec.Compute.MemoryMiB / 1024
	if ramG < 1 {
		ramG = 1
	}
	disk := strconv.Itoa(spec.Disk.SizeGiB) + "G"
	d := spec.Dockur
	if d == nil {
		d = &model.DockurOptions{Username: "Docker", Hostname: spec.Name}
	}
	bind := ports.Bind
	if bind == "" {
		bind = "127.0.0.1"
	}
	lay, _ := layoutFor(d)

	ver := version
	if u := strings.TrimSpace(d.CustomISO); u != "" && (strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://")) {
		ver = u
	}

	var env []string
	add := func(k, v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		env = append(env, fmt.Sprintf("      %s: %s", k, yamlQuote(v)))
	}
	add("VERSION", ver)
	add("RAM_SIZE", fmt.Sprintf("%dG", ramG))
	add("CPU_CORES", strconv.Itoa(spec.Compute.CPU))
	add("DISK_SIZE", disk)
	add("USERNAME", d.Username)
	add("PASSWORD", d.Password)
	add("HOST", d.Hostname)
	add("LANGUAGE", d.Language)
	add("REGION", d.Region)
	add("KEYBOARD", d.Keyboard)
	add("KEY", d.ProductKey)
	add("DOMAIN", d.Domain)
	add("DOMAIN_OU", d.DomainOU)
	add("EDITION", d.Edition)
	add("COMMAND", d.Command)
	if d.Audio {
		add("AUDIO", "Y")
	}
	if d.SecureBoot {
		add("BOOT_MODE", "windows_secure")
		add("TPM", "Y")
	}
	if d.Autologin != nil && !*d.Autologin {
		add("AUTOLOGIN", "N")
	}
	for i, g := range d.ExtraDisksGiB {
		add(fmt.Sprintf("DISK%d_SIZE", i+2), strconv.Itoa(g)+"G")
	}

	var vols []string
	vols = append(vols, "      - ./storage:/storage")
	if lay.Shared != "" {
		vols = append(vols, fmt.Sprintf("      - %s", yamlQuote("./"+lay.Shared+":/shared")))
	}
	if lay.Oem != "" {
		vols = append(vols, fmt.Sprintf("      - %s", yamlQuote("./"+lay.Oem+":/oem")))
	}
	if iso := strings.TrimSpace(d.CustomISO); iso != "" && !strings.HasPrefix(iso, "http://") && !strings.HasPrefix(iso, "https://") {
		vols = append(vols, fmt.Sprintf("      - %s", yamlQuote(iso+":/custom.iso")))
	}
	for i := range d.ExtraDisksGiB {
		vols = append(vols, fmt.Sprintf("      - ./storage%d:/storage%d", i+2, i+2))
	}

	return fmt.Sprintf(`services:
  windows:
    image: docker.io/dockurr/windows
    environment:
%s
    devices:
      - /dev/kvm
      - /dev/net/tun
    cap_add:
      - NET_ADMIN
    ports:
      - "%s:%d:8006"
      - "%s:%d:3389/tcp"
      - "%s:%d:3389/udp"
    volumes:
%s
    restart: unless-stopped
    stop_grace_period: 2m
`, strings.Join(env, "\n"), bind, ports.HTTP, bind, ports.RDP, bind, ports.RDP, strings.Join(vols, "\n"))
}

func clone(m model.Machine) *model.Machine {
	c := m
	c.IPAddresses = append([]string(nil), m.IPAddresses...)
	c.Conditions = append([]model.Condition(nil), m.Conditions...)
	if m.ProgressPercent != nil {
		v := *m.ProgressPercent
		c.ProgressPercent = &v
	}
	if m.Spec.Labels != nil {
		c.Spec.Labels = map[string]string{}
		for k, v := range m.Spec.Labels {
			c.Spec.Labels[k] = v
		}
	}
	if m.Spec.Dockur != nil {
		d := *m.Spec.Dockur
		d.Password = "" // never return guest password over the API
		if len(m.Spec.Dockur.ExtraDisksGiB) > 0 {
			d.ExtraDisksGiB = append([]int(nil), m.Spec.Dockur.ExtraDisksGiB...)
		}
		c.Spec.Dockur = &d
	}
	c.GeneratedPassword = ""
	return &c
}

type persisted struct {
	Machines map[string]map[string]model.Machine `json:"machines"`
	Ports    map[string]portPair                 `json:"ports"`
}

func (p *Provider) statePath() string { return filepath.Join(p.dataDir, "state.json") }

func (p *Provider) saveStateLocked() error {
	b, err := json.MarshalIndent(persisted{Machines: p.machines, Ports: p.ports}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(p.statePath(), b, 0o600)
}

func (p *Provider) loadState() error {
	b, err := os.ReadFile(p.statePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var s persisted
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s.Machines != nil {
		p.machines = s.Machines
	}
	if s.Ports != nil {
		p.ports = s.Ports
	}
	for project, machines := range p.machines {
		for id, m := range machines {
			p.normalizeConsole(&m)
			p.machines[project][id] = m
		}
	}
	return nil
}
