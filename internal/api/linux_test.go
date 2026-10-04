// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/model"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLinuxTemplatesAPICreate(t *testing.T) {
	cat, err := catalog.Load("")
	if err != nil {
		t.Fatal(err)
	}
	for _, img := range cat.List() {
		if img.OS != "linux" {
			continue
		}
		t.Run(img.ID, func(t *testing.T) {
			h, _ := testServer(t)
			spec := model.MachineSpec{Name: "linux-api", Image: img.ID, Compute: model.ComputeSpec{CPU: 2, MemoryMiB: 2048}, Disk: model.DiskSpec{SizeGiB: 20}, Initialization: &model.Initialization{Username: "tester"}}
			b, err := json.Marshal(spec)
			if err != nil {
				t.Fatal(err)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/machines", bytes.NewReader(b)))
			if w.Code != http.StatusCreated {
				t.Fatalf("status %d: %s", w.Code, w.Body.String())
			}
			var m model.Machine
			if err = json.Unmarshal(w.Body.Bytes(), &m); err != nil {
				t.Fatal(err)
			}
			if m.Spec.Initialization == nil || m.Spec.Initialization.Username != "tester" {
				t.Fatal("initialization lost")
			}
		})
	}
}
func TestWindowsRejectsLinuxInitialization(t *testing.T) {
	h, _ := testServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/machines", bytes.NewBufferString(`{"name":"windows","image":"windows-server-2025","compute":{"cpu":4,"memoryMiB":8192},"disk":{"sizeGiB":80},"initialization":{"username":"linux"}}`)))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("accepted Linux initialization for Windows: %s", w.Body.String())
	}
}
