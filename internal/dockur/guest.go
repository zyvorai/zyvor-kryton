// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package dockur

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

// machineInfo is a lock-free snapshot of what the guest operations need.
type machineInfo struct {
	m     model.Machine
	dir   string
	ports portPair
	lay   layout
}

func (p *Provider) lookup(project, machineID string) (machineInfo, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.machines[project][machineID]
	if !ok {
		return machineInfo{}, provider.ErrNotFound
	}
	return machineInfo{m: m, dir: p.machineDir(project, machineID), ports: p.ports[machineID], lay: layoutOf(m)}, nil
}

func (p *Provider) setState(project, machineID string, f func(m *model.Machine)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, ok := p.machines[project][machineID]
	if !ok {
		return
	}
	f(&m)
	m.UpdatedAt = time.Now().UTC()
	p.machines[project][machineID] = m
	_ = p.saveStateLocked()
}

// Restart reboots the guest. By default it runs `compose stop -t 120`,
// which makes dockur turn SIGTERM into an ACPI power-button press and wait
// for Windows to shut down cleanly (stop_grace_period caps the wait), then
// `compose start`. Force skips the ACPI shutdown (`compose kill`). With
// Wait it blocks until RDP answers (and the guest agent heartbeat is
// present for jobAgent machines) or opts.Timeout elapses.
func (p *Provider) Restart(ctx context.Context, project, machineID string, opts provider.RestartOptions) (*model.Machine, *model.Readiness, error) {
	mi, err := p.lookup(project, machineID)
	if err != nil {
		return nil, nil, err
	}
	p.setState(project, machineID, func(m *model.Machine) {
		m.State = model.StateStopping
		m.Message = "Restarting: shutting the guest down"
	})
	stop := []string{"stop", "-t", "120"}
	if opts.Force {
		stop = []string{"kill"}
	}
	if err := p.compose(ctx, mi.dir, stop...); err != nil {
		p.setState(project, machineID, func(m *model.Machine) { m.State = model.StateFailed; m.Message = err.Error() })
		return nil, nil, err
	}
	if err := p.compose(ctx, mi.dir, "start"); err != nil {
		p.setState(project, machineID, func(m *model.Machine) { m.State = model.StateFailed; m.Message = err.Error() })
		return nil, nil, err
	}
	p.setState(project, machineID, func(m *model.Machine) {
		m.State = model.StateStarting
		m.ProgressPercent = nil
		m.Message = "Restarted; waiting for the guest to answer on RDP"
	})
	var rd *model.Readiness
	if opts.Wait {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 5 * time.Minute
		}
		if rd, err = p.WaitReady(ctx, project, machineID, timeout); err != nil {
			return nil, nil, err
		}
	}
	p.mu.Lock()
	m := p.machines[project][machineID]
	p.mu.Unlock()
	return clone(m), rd, nil
}

// WaitReady polls Ready until it reports ready or timeout passes; the
// last (possibly not ready) reading is returned in both cases. It returns
// an error only for unknown machines or a cancelled ctx.
func (p *Provider) WaitReady(ctx context.Context, project, machineID string, timeout time.Duration) (*model.Readiness, error) {
	deadline := time.Now().Add(timeout)
	for {
		rd, err := p.Ready(ctx, project, machineID)
		if err != nil {
			return nil, err
		}
		if rd.Ready || !time.Now().Add(p.pollEvery).Before(deadline) {
			return rd, nil
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(p.pollEvery):
		}
	}
}

// Ready probes the guest: container running, RDP answering an X.224
// negotiation (a published port alone proves nothing, since the container
// runtime listens on it regardless of the guest), and for jobAgent
// machines a fresh agent heartbeat file. While not ready it also reads the
// container log for install progress.
func (p *Provider) Ready(ctx context.Context, project, machineID string) (*model.Readiness, error) {
	mi, err := p.lookup(project, machineID)
	if err != nil {
		return nil, err
	}
	d := mi.m.Spec.Dockur
	rd := &model.Readiness{MachineID: machineID, Checks: []model.ReadinessCheck{}}
	out, cerr := p.composeOut(ctx, mi.dir, "ps", "--status", "running", "-q")
	rd.ContainerUp = cerr == nil && strings.TrimSpace(out) != ""
	detail := ""
	if cerr != nil {
		detail = cerr.Error()
	}
	rd.Checks = append(rd.Checks, model.ReadinessCheck{Name: "container", OK: rd.ContainerUp, Detail: detail})

	if rd.ContainerUp && mi.ports.RDP != 0 {
		rd.RDPOpen = probeRDP(ctx, p.dial, net.JoinHostPort("127.0.0.1", strconv.Itoa(mi.ports.RDP)))
	}
	rd.Checks = append(rd.Checks, model.ReadinessCheck{Name: "rdp", OK: rd.RDPOpen})

	rd.AgentRequired = d != nil && d.JobAgent
	if rd.AgentRequired && mi.lay.Shared != "" {
		hb := filepath.Join(mi.dir, mi.lay.Shared, ".kryton", "agent-ready")
		if fi, err := os.Lstat(hb); err == nil && fi.Mode().IsRegular() && time.Since(fi.ModTime()) < p.agentFresh {
			rd.AgentPresent = true
		}
		rd.Checks = append(rd.Checks, model.ReadinessCheck{Name: "agent", OK: rd.AgentPresent, Detail: "heartbeat file .kryton/agent-ready"})
	}
	rd.Ready = rd.ContainerUp && rd.RDPOpen && (!rd.AgentRequired || rd.AgentPresent)

	if rd.Ready {
		rd.Install = &model.InstallProgress{Phase: "complete", Percent: 100, Message: "Windows is installed and answering on RDP"}
		p.setState(project, machineID, func(m *model.Machine) {
			if m.State == model.StateProvisioning || m.State == model.StateStarting {
				m.State = model.StateRunning
				pct := 100
				m.ProgressPercent = &pct
				m.Message = "Guest is ready"
			}
		})
	} else if rd.ContainerUp {
		if logs, err := p.composeOut(ctx, mi.dir, "logs", "--no-color", "--tail", "200"); err == nil {
			ip := parseInstallProgress(logs)
			rd.Install = &ip
		}
	}
	return rd, nil
}

// Logs returns the last tail lines of the dockur container log.
func (p *Provider) Logs(ctx context.Context, project, machineID string, tail int) (string, error) {
	mi, err := p.lookup(project, machineID)
	if err != nil {
		return "", err
	}
	if tail < 1 {
		tail = 200
	}
	if tail > 2000 {
		tail = 2000
	}
	out, err := p.composeOut(ctx, mi.dir, "logs", "--no-color", "--tail", strconv.Itoa(tail))
	if err != nil {
		return "", err
	}
	return stripANSI(out), nil
}

// x224ConnReq is an RDP X.224 Connection Request with an RDP_NEG_REQ.
var x224ConnReq = []byte{0x03, 0x00, 0x00, 0x13, 0x0e, 0xe0, 0x00, 0x00, 0x00, 0x00, 0x00, 0x01, 0x00, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00}

// probeRDP reports whether something speaking RDP (TPKT) answers at addr.
func probeRDP(ctx context.Context, dial func(context.Context, string) (net.Conn, error), addr string) bool {
	cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	conn, err := dial(cctx, addr)
	if err != nil {
		return false
	}
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := conn.Write(x224ConnReq); err != nil {
		return false
	}
	var hdr [4]byte
	n := 0
	for n < len(hdr) {
		k, err := conn.Read(hdr[n:])
		n += k
		if err != nil {
			break
		}
	}
	return n >= 2 && hdr[0] == 0x03 && hdr[1] == 0x00
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]`)
var pctRE = regexp.MustCompile(`(\d{1,3})(?:\.\d+)?\s*%`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }

// parseInstallProgress turns dockur's container log into a coarse phase
// and percentage. It is a heuristic over dockur's human-readable messages
// ("Downloading ...", "Extracting ...", "Building image...", "Windows
// started successfully"): the newest recognised line wins. Percentages
// are bands, not measurements: download 5-45, extract 50, build 60,
// Windows booting/installing 70 (100 comes only from the RDP probe).
func parseInstallProgress(logs string) model.InstallProgress {
	lines := strings.Split(stripANSI(logs), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		raw := strings.TrimSpace(strings.TrimLeft(lines[i], "❯ \t\r"))
		if idx := strings.Index(raw, "| "); idx >= 0 && idx < 40 { // compose "service  | " prefix
			raw = strings.TrimSpace(strings.TrimLeft(raw[idx+2:], "❯ \t\r"))
		}
		l := strings.ToLower(raw)
		switch {
		case l == "":
			continue
		case strings.Contains(l, "shutdown completed") || strings.Contains(l, "shutting down"):
			return model.InstallProgress{Phase: "stopped", Percent: 0, Message: raw}
		case strings.Contains(l, "started successfully") || strings.Contains(l, "booting") || strings.Contains(l, "windows started"):
			return model.InstallProgress{Phase: "installing", Percent: 70, Message: "Windows is booting / running its unattended setup (watch the web viewer)"}
		case strings.Contains(l, "building image") || strings.Contains(l, "adding xml") || strings.Contains(l, "patching") || strings.Contains(l, "creating") || strings.Contains(l, "converting"):
			return model.InstallProgress{Phase: "building", Percent: 60, Message: raw}
		case strings.Contains(l, "extracting") || strings.Contains(l, "unpacking") || strings.Contains(l, "copying"):
			return model.InstallProgress{Phase: "extracting", Percent: 50, Message: raw}
		case strings.Contains(l, "download"):
			pct := 5
			if m := pctRE.FindStringSubmatch(l); m != nil {
				if v, err := strconv.Atoi(m[1]); err == nil && v <= 100 {
					pct = 5 + v*40/100
				}
			}
			return model.InstallProgress{Phase: "downloading", Percent: pct, Message: raw}
		case strings.Contains(l, "starting windows") || strings.Contains(l, "starting"):
			return model.InstallProgress{Phase: "starting", Percent: 3, Message: raw}
		}
	}
	return model.InstallProgress{Phase: "unknown", Percent: 1, Message: "no progress information in the container log yet"}
}

// RunMonitor refreshes unfinished guest jobs every interval until ctx is
// done, so completion events fire even when nobody polls the API.
func (p *Provider) RunMonitor(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 5 * time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.scanJobs(ctx)
		}
	}
}

func (p *Provider) scanJobs(ctx context.Context) {
	type ref struct{ project, id string }
	var refs []ref
	p.mu.Lock()
	for project, ms := range p.machines {
		for id, m := range ms {
			if m.Spec.Dockur != nil && m.Spec.Dockur.JobAgent {
				refs = append(refs, ref{project, id})
			}
		}
	}
	p.mu.Unlock()
	for _, r := range refs {
		if _, err := p.ListJobs(ctx, r.project, r.id); err != nil && ctx.Err() != nil {
			return
		}
	}
}

func errf(sentinel error, format string, a ...any) error {
	return fmt.Errorf("%w: %s", sentinel, fmt.Sprintf(format, a...))
}
