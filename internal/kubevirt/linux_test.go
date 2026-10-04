// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package kubevirt

import (
	"context"
	"encoding/json"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/kubeapi"
	"github.com/zyvorai/kryton/internal/model"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise Create through the real Kubernetes REST client for every catalog
// entry, checking Linux initialization and preservation of Windows hardware.
func TestAllCatalogTemplatesCreate(t *testing.T) {
	cat, err := catalog.Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, img := range cat.List() {
		t.Run(img.ID, func(t *testing.T) {
			var created map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/virtualmachines") {
					if err := json.NewDecoder(r.Body).Decode(&created); err != nil {
						t.Error(err)
					}
					_ = json.NewEncoder(w).Encode(created)
					return
				}
				if r.Method == "GET" {
					_, _ = w.Write([]byte(`{"metadata":{"name":"default"}}`))
					return
				}
				_, _ = w.Write([]byte(`{}`))
			}))
			defer server.Close()
			client, err := kubeapi.New(kubeapi.Config{Endpoint: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			p := New(Config{Client: client, Catalog: cat, ImageNamespace: "images"})
			spec := model.MachineSpec{Name: "test-guest", Image: img.ID, Compute: model.ComputeSpec{CPU: img.MinCPU, MemoryMiB: img.MinMemoryMiB}, Disk: model.DiskSpec{SizeGiB: img.DefaultDiskGB}}
			if img.OS == "linux" {
				spec.Initialization = &model.Initialization{Username: "tester", SSHAuthorizedKeys: []string{"ssh-ed25519 AAAA comment"}}
			}
			if _, err = p.Create(context.Background(), "default", spec); err != nil {
				t.Fatal(err)
			}
			guest := created["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			domain := guest["domain"].(map[string]any)
			devices := domain["devices"].(map[string]any)
			_, tpm := devices["tpm"]
			_, hyperv := domain["features"].(map[string]any)["hyperv"]
			volumes := guest["volumes"].([]any)
			if img.OS == "linux" {
				if tpm || hyperv {
					t.Fatal("Linux inherited Windows hardware")
				}
				if len(volumes) != 2 {
					t.Fatalf("missing seed volume: %v", volumes)
				}
				data := volumes[1].(map[string]any)["cloudInitNoCloud"].(map[string]any)["userData"].(string)
				var config map[string]any
				if err = json.Unmarshal([]byte(strings.TrimPrefix(data, "#cloud-config\n")), &config); err != nil {
					t.Fatal(err)
				}
				if config["ssh_pwauth"] != false {
					t.Fatal("password authentication enabled")
				}
				user := config["users"].([]any)[0].(map[string]any)
				if user["name"] != "tester" {
					t.Fatal("username lost")
				}
				if _, ok := domain["firmware"].(map[string]any)["bootloader"].(map[string]any)["bios"]; !ok {
					t.Fatal("BIOS profile lost")
				}
			} else {
				if !tpm || !hyperv || len(volumes) != 1 {
					t.Fatal("Windows hardware regression")
				}
			}
		})
	}
}
