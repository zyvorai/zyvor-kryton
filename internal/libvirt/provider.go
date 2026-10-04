// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

// Package libvirt implements local libvirt machine lifecycle through virsh.
// It never invokes a shell; disks and seed media live in a managed directory.
package libvirt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/id"
	"github.com/zyvorai/kryton/internal/imagebuild"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Runner interface {
	Run(context.Context, string, ...string) ([]byte, error)
}
type commandRunner struct{}

func (commandRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	// Provider-selected tools only; arguments are passed directly, never to a shell.
	switch name {
	case "virsh", "qemu-img", "genisoimage":
	default:
		return nil, fmt.Errorf("unsupported host tool")
	}
	cmd := exec.CommandContext(ctx, name, args...) // #nosec G204 -- fixed allowlist above; no shell expansion
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("%s failed: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

type Config struct {
	URI, DataDir, ImageDir, Network string
	Catalog                         *catalog.Catalog
	Runner                          Runner
}
type Provider struct {
	cfg Config
	mu  sync.Mutex
}

var _ provider.Provider = (*Provider)(nil)
var uuidRE = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

func New(c Config) (*Provider, error) {
	if c.URI == "" {
		c.URI = "qemu:///system"
	}
	if c.URI != "qemu:///system" && c.URI != "qemu:///session" {
		return nil, fmt.Errorf("only local libvirt URIs supported")
	}
	if c.DataDir == "" || c.ImageDir == "" || c.Catalog == nil {
		return nil, fmt.Errorf("data directory, image directory and catalog required")
	}
	var err error
	c.DataDir, err = filepath.Abs(c.DataDir)
	if err != nil {
		return nil, err
	}
	c.ImageDir, err = filepath.Abs(c.ImageDir)
	if err != nil {
		return nil, err
	}
	if c.Network == "" {
		c.Network = "default"
	}
	if c.Runner == nil {
		c.Runner = commandRunner{}
	}
	if err = os.MkdirAll(c.DataDir, 0750); err != nil {
		return nil, err
	}
	return &Provider{cfg: c}, nil
}
func (p *Provider) Name() string { return "libvirt" }
func (p *Provider) virsh(ctx context.Context, args ...string) ([]byte, error) {
	return p.cfg.Runner.Run(ctx, "virsh", append([]string{"--connect", p.cfg.URI}, args...)...)
}
func (p *Provider) Health(ctx context.Context) error { _, err := p.virsh(ctx, "version"); return err }
func (p *Provider) Capabilities(context.Context) (model.Capabilities, error) {
	return model.Capabilities{Provider: p.Name(), Networks: true, TTL: true}, nil
}
func (p *Provider) Create(ctx context.Context, project string, spec model.MachineSpec) (*model.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := model.ValidateProject(project); err != nil {
		return nil, err
	}
	if err := model.ValidateMachineSpec(spec); err != nil {
		return nil, err
	}
	img, ok := p.cfg.Catalog.Get(spec.Image)
	if !ok || !img.SupportsProvider(p.Name()) || img.OS != "linux" {
		return nil, fmt.Errorf("linux libvirt image required")
	}
	if spec.Dockur != nil || spec.Disk.StorageClass != "" || spec.Disk.VolumeMode != "" {
		return nil, fmt.Errorf("dockur and Kubernetes disk options are unsupported by libvirt")
	}
	if spec.Compute.CPU < img.MinCPU || spec.Compute.MemoryMiB < img.MinMemoryMiB {
		return nil, fmt.Errorf("image minimum resources not met")
	}
	items, err := p.list(ctx, project)
	if err != nil {
		return nil, err
	}
	for _, m := range items {
		if m.Spec.Name == spec.Name {
			return nil, provider.ErrConflict
		}
	}
	art, err := (imagebuild.Store{Dir: p.cfg.ImageDir}).Get(spec.Image)
	if err != nil {
		return nil, fmt.Errorf("acquire image first: %w", err)
	}
	// Reject external backing chains before asking qemu-img to copy the image.
	info, err := p.cfg.Runner.Run(ctx, "qemu-img", "info", "--output=json", art.Path)
	if err != nil {
		return nil, err
	}
	var imageInfo struct {
		Format      string `json:"format"`
		Backing     string `json:"backing-filename"`
		VirtualSize int64  `json:"virtual-size"`
	}
	if err = json.Unmarshal(info, &imageInfo); err != nil {
		return nil, err
	}
	if imageInfo.Format != "qcow2" || imageInfo.Backing != "" {
		return nil, fmt.Errorf("standalone QCOW2 source required")
	}
	if imageInfo.VirtualSize > int64(spec.Disk.SizeGiB)<<30 {
		return nil, fmt.Errorf("requested disk is smaller than source image")
	}
	machineID := id.New()
	name := "kryton-" + machineID
	dir := filepath.Join(p.cfg.DataDir, project, machineID)
	if err = os.MkdirAll(dir, 0750); err != nil {
		return nil, err
	}
	defined := false
	keep := false
	defer func() {
		if !defined && !keep {
			_ = os.RemoveAll(dir)
		}
	}()
	now := time.Now().UTC()
	m := model.Machine{ID: machineID, Project: project, Provider: p.Name(), State: model.StateProvisioning, Spec: spec, ProviderRef: model.ProviderRef{Provider: p.Name(), Name: name}, CreatedAt: now, UpdatedAt: now}
	if spec.TTLMinutes > 0 {
		x := now.Add(time.Duration(spec.TTLMinutes) * time.Minute)
		m.ExpiresAt = &x
	}
	disk := filepath.Join(dir, "root.qcow2")
	seed := filepath.Join(dir, "seed.iso")
	if _, err = p.cfg.Runner.Run(ctx, "qemu-img", "convert", "-f", "qcow2", "-O", "qcow2", art.Path, disk); err != nil {
		return nil, err
	}
	// #nosec G302 -- managed VM disk needs write access for the shared QEMU group
	if err = os.Chmod(disk, 0660); err != nil {
		return nil, err
	}
	if _, err = p.cfg.Runner.Run(ctx, "qemu-img", "resize", disk, fmt.Sprintf("%dG", spec.Disk.SizeGiB)); err != nil {
		return nil, err
	}
	userData, err := model.CloudConfig(img, spec)
	if err != nil {
		return nil, err
	}
	meta, _ := json.Marshal(map[string]string{"instance-id": machineID, "local-hostname": spec.Name})
	// #nosec G306 -- public SSH initialization; group-readable seed staging
	if err = os.WriteFile(filepath.Join(dir, "user-data"), []byte(userData), 0640); err != nil {
		return nil, err
	}
	// #nosec G306 -- nonsecret instance identity for seed staging
	if err = os.WriteFile(filepath.Join(dir, "meta-data"), meta, 0640); err != nil {
		return nil, err
	}
	if _, err = p.cfg.Runner.Run(ctx, "genisoimage", "-output", seed, "-volid", "cidata", "-joliet", "-rock", filepath.Join(dir, "user-data"), filepath.Join(dir, "meta-data")); err != nil {
		return nil, err
	}
	// #nosec G302 -- seed contains public SSH keys; shared QEMU group needs read access
	if err = os.Chmod(seed, 0640); err != nil {
		return nil, err
	}
	network := p.cfg.Network
	if spec.Network.NetworkID != "" {
		network = spec.Network.NetworkID
	}
	domain, err := DomainXML(m, img, disk, seed, network)
	if err != nil {
		return nil, err
	}
	xmlPath := filepath.Join(dir, "domain.xml")
	if err = os.WriteFile(xmlPath, []byte(domain), 0600); err != nil {
		return nil, err
	}
	if err = p.save(m); err != nil {
		return nil, err
	}
	if _, err = p.virsh(ctx, "define", xmlPath); err != nil {
		keep = true
		return nil, err
	}
	defined = true
	if _, err = p.virsh(ctx, "start", name); err != nil {
		m.State = model.StateFailed
		m.Message = "Domain defined but start failed; correct host configuration then retry Start"
		_ = p.save(m)
		return nil, err
	}
	m.State = model.StateStarting
	if err = p.save(m); err != nil {
		return nil, err
	}
	return &m, nil
}
func (p *Provider) dir(project, machineID string) (string, error) {
	if err := model.ValidateProject(project); err != nil {
		return "", err
	}
	if !uuidRE.MatchString(machineID) {
		return "", provider.ErrNotFound
	}
	return filepath.Join(p.cfg.DataDir, project, machineID), nil
}
func (p *Provider) save(m model.Machine) error {
	dir, err := p.dir(m.Project, m.ID)
	if err != nil {
		return err
	}
	b, err := json.Marshal(m)
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "machine.json")
	if err = os.WriteFile(path+".tmp", b, 0600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}
func (p *Provider) get(ctx context.Context, project, machineID string) (*model.Machine, error) {
	dir, err := p.dir(project, machineID)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(filepath.Join(dir, "machine.json")) // #nosec G304 -- dir() validates project/UUID beneath operator-controlled DataDir
	if os.IsNotExist(err) {
		return nil, provider.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	var m model.Machine
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	if m.ID != machineID || m.Project != project || m.ProviderRef.Name != "kryton-"+machineID {
		return nil, fmt.Errorf("invalid managed machine record")
	}
	out, err := p.virsh(ctx, "domstate", m.ProviderRef.Name)
	if err != nil {
		m.State = model.StateUnknown
		m.Message = "libvirt state unavailable"
		return &m, nil
	}
	switch strings.TrimSpace(string(out)) {
	case "running":
		m.State = model.StateRunning
	case "shut off":
		m.State = model.StateStopped
	case "paused":
		m.State = model.StatePaused
	case "in shutdown":
		m.State = model.StateStopping
	case "crashed":
		m.State = model.StateFailed
	default:
		m.State = model.StateUnknown
	}
	m.UpdatedAt = time.Now().UTC()
	out, err = p.virsh(ctx, "domifaddr", m.ProviderRef.Name, "--source", "agent")
	if err == nil {
		m.IPAddresses = guestAddresses(out)
	}
	return &m, nil
}

// guestAddresses parses `virsh domifaddr --source agent` output. The agent
// reports every interface, so loopback and link-local addresses are dropped
// and IPv4 is listed first: clients SSH to IPAddresses[0].
func guestAddresses(out []byte) []string {
	var v4, v6 []string
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 || (fields[2] != "ipv4" && fields[2] != "ipv6") {
			continue
		}
		ip := net.ParseIP(strings.Split(fields[3], "/")[0])
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() {
			continue
		}
		if ip.To4() != nil {
			v4 = append(v4, ip.String())
		} else {
			v6 = append(v6, ip.String())
		}
	}
	return append(v4, v6...)
}
func (p *Provider) Get(ctx context.Context, project, machineID string) (*model.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.get(ctx, project, machineID)
}
func (p *Provider) list(ctx context.Context, project string) ([]model.Machine, error) {
	if err := model.ValidateProject(project); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(filepath.Join(p.cfg.DataDir, project))
	if os.IsNotExist(err) {
		return []model.Machine{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []model.Machine{}
	for _, e := range entries {
		if e.IsDir() && uuidRE.MatchString(e.Name()) {
			m, err := p.get(ctx, project, e.Name())
			if err != nil {
				return nil, err
			}
			out = append(out, *m)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
}
func (p *Provider) List(ctx context.Context, project string) ([]model.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.list(ctx, project)
}
func (p *Provider) power(ctx context.Context, project, machineID, action string) (*model.Machine, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, err := p.get(ctx, project, machineID)
	if err != nil {
		return nil, err
	}
	if (action == "start" && m.State == model.StateRunning) || (action == "shutdown" && m.State == model.StateStopped) {
		return m, nil
	}
	if _, err = p.virsh(ctx, action, m.ProviderRef.Name); err != nil {
		return nil, err
	}
	return p.get(ctx, project, machineID)
}
func (p *Provider) Start(ctx context.Context, project, machineID string) (*model.Machine, error) {
	return p.power(ctx, project, machineID, "start")
}
func (p *Provider) Stop(ctx context.Context, project, machineID string) (*model.Machine, error) {
	return p.power(ctx, project, machineID, "shutdown")
}
func (p *Provider) Delete(ctx context.Context, project, machineID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	m, err := p.get(ctx, project, machineID)
	if errors.Is(err, provider.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if m.State == model.StateUnknown {
		return fmt.Errorf("refusing deletion while domain state is unknown")
	}
	if m.State != model.StateStopped {
		if _, err = p.virsh(ctx, "destroy", m.ProviderRef.Name); err != nil {
			return err
		}
	}
	if _, err = p.virsh(ctx, "undefine", m.ProviderRef.Name, "--nvram"); err != nil {
		return err
	}
	dir, err := p.dir(project, machineID)
	if err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// Snapshot support is deliberately not advertised: disk-only copies of running
// VMs would be unsafe and require a separate quiesce/restore implementation.
func (p *Provider) Snapshot(context.Context, string, string, string) (*model.Snapshot, error) {
	return nil, provider.ErrUnsupported
}
func (p *Provider) ListSnapshots(context.Context, string, string) ([]model.Snapshot, error) {
	return nil, provider.ErrUnsupported
}
func (p *Provider) RestoreSnapshot(context.Context, string, string, string) (*model.Snapshot, error) {
	return nil, provider.ErrUnsupported
}
func (p *Provider) DeleteSnapshot(context.Context, string, string, string) error {
	return provider.ErrUnsupported
}
