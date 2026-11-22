// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package dockur

import (
	"strings"
	"testing"

	"github.com/zyvorai/kryton/internal/model"
)

type mapResolver map[string]string

func (m mapResolver) DockurVersion(id string) (string, bool) {
	v, ok := m[id]
	return v, ok
}

func TestResolveDefaultVersions(t *testing.T) {
	p := &Provider{catalog: nil}
	v, ok := p.resolveVersion("windows-server-2025")
	if !ok || v != "2025" {
		t.Fatalf("got %q %v", v, ok)
	}
	v, ok = p.resolveVersion("windows-11-enterprise")
	if !ok || v != "11e" {
		t.Fatalf("got %q %v", v, ok)
	}
	v, ok = p.resolveVersion("windows-tiny11")
	if !ok || v != "tiny11" {
		t.Fatalf("got %q %v", v, ok)
	}
}

func TestCatalogOverridesDefault(t *testing.T) {
	p := &Provider{catalog: mapResolver{"windows-server-2025": "2025"}}
	v, ok := p.resolveVersion("windows-server-2025")
	if !ok || v != "2025" {
		t.Fatalf("got %q %v", v, ok)
	}
}

func TestConsoleURLAndRDP(t *testing.T) {
	p := &Provider{
		publicHost: "lab.example",
		ports:      map[string]portPair{"m1": {HTTP: 18006, RDP: 13389}},
	}
	m := &model.Machine{ID: "m1", Project: "default", Spec: model.MachineSpec{Dockur: &model.DockurOptions{Username: "lab"}}}
	p.normalizeConsole(m)
	want := "/api/v1/machines/m1/console/?project=default"
	if m.ConsoleURL != want {
		t.Fatalf("consoleUrl = %q, want %q", m.ConsoleURL, want)
	}
	if m.RdpHost != "lab.example" || m.RdpPort != 13389 || m.RdpUsername != "lab" {
		t.Fatalf("rdp = %s:%d user=%s", m.RdpHost, m.RdpPort, m.RdpUsername)
	}
	// A loopback-bound machine advertises the loopback address, not the public host.
	p.ports["m1"] = portPair{HTTP: 18006, RDP: 13389, Bind: "127.0.0.1"}
	p.normalizeConsole(m)
	if m.RdpHost != "127.0.0.1" {
		t.Fatalf("rdpHost = %q for loopback bind", m.RdpHost)
	}
}

func TestRenderComposeBasics(t *testing.T) {
	spec := model.MachineSpec{Name: "win1", Image: "windows-11-enterprise", Compute: model.ComputeSpec{CPU: 4, MemoryMiB: 8192}, Disk: model.DiskSpec{SizeGiB: 64}}
	applied := applyDockurDefaults(spec)
	if applied.Dockur.Password == "" || applied.Dockur.Password == "admin" {
		t.Fatalf("default password must be generated, got %q", applied.Dockur.Password)
	}
	out := renderCompose(applied, "11e", portPair{HTTP: 18006, RDP: 13389})
	for _, want := range []string{`VERSION: "11e"`, `RAM_SIZE: "8G"`, `CPU_CORES: "4"`, `DISK_SIZE: "64G"`, `USERNAME: "Docker"`, `PASSWORD: "` + applied.Dockur.Password + `"`, `HOST: "win1"`, `127.0.0.1:18006:8006`, `127.0.0.1:13389:3389`} {
		if !strings.Contains(out, want) {
			t.Fatalf("compose missing %q\n%s", want, out)
		}
	}
}

func TestRenderComposeRichOptions(t *testing.T) {
	auto := false
	spec := model.MachineSpec{
		Name: "lab", Image: "windows-11-pro",
		Compute: model.ComputeSpec{CPU: 2, MemoryMiB: 4096}, Disk: model.DiskSpec{SizeGiB: 40},
		Dockur: &model.DockurOptions{
			Username: "bill", Password: "gates", Language: "French", Region: "fr-FR", Keyboard: "fr-FR",
			ProductKey: "AAAAA-BBBBB-CCCCC-DDDDD-EEEEE", Domain: "example.com", DomainOU: "OU=Labs,DC=example,DC=com",
			Autologin: &auto, Audio: true, SecureBoot: true,
			SharedDir: "shared", OemDir: "oem", Command: "echo hi",
			ExtraDisksGiB: []int{32, 64}, Edition: "core",
		},
	}
	out := renderCompose(applyDockurDefaults(spec), "11", portPair{HTTP: 18007, RDP: 13390})
	for _, want := range []string{
		`USERNAME: "bill"`, `PASSWORD: "gates"`, `LANGUAGE: "French"`, `AUDIO: "Y"`,
		`BOOT_MODE: "windows_secure"`, `TPM: "Y"`, `AUTOLOGIN: "N"`,
		`DISK2_SIZE: "32G"`, `DISK3_SIZE: "64G"`, `./storage2:/storage2`,
		`"./shared:/shared"`, `"./oem:/oem"`, `DOMAIN: "example.com"`, `EDITION: "core"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("compose missing %q\n%s", want, out)
		}
	}
}

func TestCloneRedactsPassword(t *testing.T) {
	m := model.Machine{Spec: model.MachineSpec{Dockur: &model.DockurOptions{Username: "u", Password: "secret"}}}
	c := clone(m)
	if c.Spec.Dockur.Password != "" {
		t.Fatal("password should be redacted")
	}
	if m.Spec.Dockur.Password != "secret" {
		t.Fatal("original password mutated")
	}
}
