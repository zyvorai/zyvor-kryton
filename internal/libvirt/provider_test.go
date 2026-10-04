// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package libvirt

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/imagebuild"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls    []string
	state    string
	fail     string
	realDisk bool
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.fail != "" && strings.Contains(strings.Join(args, " "), f.fail) {
		return nil, fmt.Errorf("injected failure")
	}
	if f.realDisk && name != "virsh" {
		return commandRunner{}.Run(ctx, name, args...)
	}
	if name == "qemu-img" {
		switch args[0] {
		case "info":
			return []byte(`{"format":"qcow2","virtual-size":1048576}`), nil
		case "convert":
			return nil, os.WriteFile(args[len(args)-1], []byte("disk"), 0640)
		}
	}
	if name == "genisoimage" {
		return nil, os.WriteFile(args[1], []byte("iso"), 0640)
	}
	if name == "virsh" {
		switch args[2] {
		case "start":
			f.state = "running"
		case "shutdown", "destroy":
			f.state = "shut off"
		case "domstate":
			return []byte(f.state), nil
		case "domifaddr":
			return []byte("vnet0 00:11:22:33:44:55 ipv4 192.0.2.1/24\n"), nil
		}
	}
	return nil, nil
}
func prepareImage(t *testing.T, dir string, img model.Image, data []byte) {
	t.Helper()
	sum := sha256.Sum256(data)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }))
	defer server.Close()
	if _, err := (imagebuild.Store{Dir: dir, Client: server.Client()}).Fetch(context.Background(), img, server.URL, hex.EncodeToString(sum[:])); err != nil {
		t.Fatal(err)
	}
}
func TestAllLinuxLibvirtTemplatesLifecycleAndRecovery(t *testing.T) {
	cat, err := catalog.Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, img := range cat.List() {
		if img.OS != "linux" {
			continue
		}
		t.Run(img.ID, func(t *testing.T) {
			root := t.TempDir()
			images := filepath.Join(root, "images")
			prepareImage(t, images, img, []byte("fixture disk"))
			runner := &fakeRunner{}
			cfg := Config{DataDir: filepath.Join(root, "machines"), ImageDir: images, Catalog: cat, Runner: runner}
			p, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			spec := model.MachineSpec{Name: "guest", Image: img.ID, Compute: model.ComputeSpec{CPU: 2, MemoryMiB: 2048}, Disk: model.DiskSpec{SizeGiB: 20}, TTLMinutes: 10}
			m, err := p.Create(ctx, "lab", spec)
			if err != nil {
				t.Fatal(err)
			}
			if m.ExpiresAt == nil {
				t.Fatal("TTL lost")
			}
			if _, err = p.Create(ctx, "lab", spec); !errors.Is(err, provider.ErrConflict) {
				t.Fatalf("duplicate create: %v", err)
			}
			restarted, err := New(cfg)
			if err != nil {
				t.Fatal(err)
			}
			got, err := restarted.Get(ctx, "lab", m.ID)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != model.StateRunning || len(got.IPAddresses) != 1 {
				t.Fatalf("recovery failed: %+v", got)
			}
			if _, err = restarted.Get(ctx, "other", m.ID); !errors.Is(err, provider.ErrNotFound) {
				t.Fatal("cross-project lookup allowed")
			}
			if _, err = restarted.Stop(ctx, "lab", m.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.Start(ctx, "lab", m.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.Snapshot(ctx, "lab", m.ID, "unsafe"); !errors.Is(err, provider.ErrUnsupported) {
				t.Fatal("snapshots advertised without quiescing")
			}
			if err = restarted.Delete(ctx, "lab", m.ID); err != nil {
				t.Fatal(err)
			}
			if _, err = restarted.Get(ctx, "lab", m.ID); !errors.Is(err, provider.ErrNotFound) {
				t.Fatal("deleted machine remains")
			}
		})
	}
}
func TestLibvirtFailureRetainsDefinedDisks(t *testing.T) {
	cat, _ := catalog.Load("")
	img, _ := cat.Get("debian-13")
	root := t.TempDir()
	images := filepath.Join(root, "images")
	prepareImage(t, images, img, []byte("image"))
	runner := &fakeRunner{fail: "start"}
	p, err := New(Config{DataDir: filepath.Join(root, "machines"), ImageDir: images, Catalog: cat, Runner: runner})
	if err != nil {
		t.Fatal(err)
	}
	spec := model.MachineSpec{Name: "guest", Image: img.ID, Compute: model.ComputeSpec{CPU: 2, MemoryMiB: 2048}, Disk: model.DiskSpec{SizeGiB: 20}}
	if _, err = p.Create(context.Background(), "lab", spec); err == nil {
		t.Fatal("ignored start failure")
	}
	entries, err := os.ReadDir(filepath.Join(root, "machines", "lab"))
	if err != nil || len(entries) != 1 {
		t.Fatal("lost recoverable record")
	}
	if _, err = os.Stat(filepath.Join(root, "machines", "lab", entries[0].Name(), "root.qcow2")); err != nil {
		t.Fatal("deleted defined domain disk")
	}
	runner.fail = "domstate"
	if err = p.Delete(context.Background(), "lab", entries[0].Name()); err == nil {
		t.Fatal("deleted unknown domain")
	}
	if _, err = p.Get(context.Background(), "../../etc", "x"); err == nil {
		t.Fatal("invalid project accepted")
	}
}
func TestAllLibvirtDomainXML(t *testing.T) {
	cat, _ := catalog.Load("")
	validator, _ := exec.LookPath("virt-xml-validate")
	for _, img := range cat.List() {
		if img.OS != "linux" {
			continue
		}
		t.Run(img.ID, func(t *testing.T) {
			m := model.Machine{ID: "00000000-0000-4000-8000-000000000001", ProviderRef: model.ProviderRef{Name: "test-guest"}, Spec: model.MachineSpec{Name: "test-guest", Image: img.ID, Compute: model.ComputeSpec{CPU: 2, MemoryMiB: 2048}, Disk: model.DiskSpec{SizeGiB: 20}}}
			text, err := DomainXML(m, img, "/images/a'&b.qcow2", "/images/seed.iso", "default")
			if err != nil {
				t.Fatal(err)
			}
			var domain struct {
				Devices struct {
					Disks []struct {
						Source struct {
							File string `xml:"file,attr"`
						} `xml:"source"`
					} `xml:"disk"`
				} `xml:"devices"`
			}
			if err = xml.Unmarshal([]byte(text), &domain); err != nil {
				t.Fatal(err)
			}
			if len(domain.Devices.Disks) != 2 || domain.Devices.Disks[0].Source.File != "/images/a'&b.qcow2" {
				t.Fatal("disk path changed")
			}
			if validator != "" {
				path := filepath.Join(t.TempDir(), "domain.xml")
				if err = os.WriteFile(path, []byte(text), 0600); err != nil {
					t.Fatal(err)
				}
				if out, err := exec.Command(validator, path, "domain").CombinedOutput(); err != nil {
					t.Fatalf("libvirt schema: %v %s", err, out)
				}
			}
		})
	}
}
func TestRealDiskAndSeedProvisioning(t *testing.T) {
	for _, tool := range []string{"qemu-img", "genisoimage", "isoinfo"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skip("disk-tool integration requires " + tool)
		}
	}
	cat, _ := catalog.Load("")
	img, _ := cat.Get("ubuntu-24.04")
	root := t.TempDir()
	source := filepath.Join(root, "source.qcow2")
	if out, err := exec.Command("qemu-img", "create", "-f", "qcow2", source, "16M").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	images := filepath.Join(root, "images")
	prepareImage(t, images, img, data)
	p, err := New(Config{DataDir: filepath.Join(root, "machines"), ImageDir: images, Catalog: cat, Runner: &fakeRunner{realDisk: true}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := p.Create(context.Background(), "lab", model.MachineSpec{Name: "disk-test", Image: img.ID, Compute: model.ComputeSpec{CPU: 1, MemoryMiB: 1024}, Disk: model.DiskSpec{SizeGiB: 20}})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "machines", "lab", m.ID)
	out, err := exec.Command("qemu-img", "info", "--output=json", filepath.Join(dir, "root.qcow2")).Output()
	if err != nil {
		t.Fatal(err)
	}
	var info struct {
		VirtualSize int64 `json:"virtual-size"`
	}
	if err = json.Unmarshal(out, &info); err != nil {
		t.Fatal(err)
	}
	if info.VirtualSize != 20<<30 {
		t.Fatal("disk not resized")
	}
	out, err = exec.Command("isoinfo", "-i", filepath.Join(dir, "seed.iso"), "-R", "-x", "/user-data").Output()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(out), "#cloud-config\n") {
		t.Fatalf("invalid NoCloud ISO: %s", out)
	}
}
