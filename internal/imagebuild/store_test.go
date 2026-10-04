// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package imagebuild

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/model"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPinnedAcquisitionAndTamperDetection(t *testing.T) {
	data := []byte("test image")
	h := sha256.Sum256(data)
	digest := hex.EncodeToString(h[:])
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(data) }))
	defer server.Close()
	store := Store{Dir: t.TempDir(), Client: server.Client()}
	img := model.Image{ID: "ubuntu-24.04", OS: "linux"}
	a, err := store.Fetch(context.Background(), img, server.URL, digest)
	if err != nil {
		t.Fatal(err)
	}
	if a.BootValidated {
		t.Fatal("acquisition must not certify boot")
	}
	if _, err = store.Get(img.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Fetch(context.Background(), img, server.URL, digest); err != nil {
		t.Fatal("cache collision failed", err)
	}
	if _, err = store.Fetch(context.Background(), img, server.URL, strings.Repeat("a", 64)); err == nil {
		t.Fatal("accepted corrupt download")
	}
	store.MaxBytes = 3
	if _, err = store.Fetch(context.Background(), img, server.URL, digest); err == nil {
		t.Fatal("size limit ignored")
	}
	if err = os.Chmod(a.Path, 0644); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(a.Path, []byte("tamper"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Get(img.ID); err == nil {
		t.Fatal("accepted corrupt cached image")
	}
}
func TestFetchFailureDoesNotPublishManifest(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "no", 404) }))
	defer server.Close()
	dir := t.TempDir()
	s := Store{Dir: dir, Client: server.Client()}
	img := model.Image{ID: "debian-13", OS: "linux"}
	for _, source := range []string{server.URL, "http://example.com", "https://user:password@example.com"} {
		if _, err := s.Fetch(context.Background(), img, source, strings.Repeat("a", 64)); err == nil {
			t.Fatal("accepted bad source")
		}
	}
	if _, err := os.Stat(filepath.Join(dir, img.ID+".json")); !os.IsNotExist(err) {
		t.Fatal("published failed artifact")
	}
	if _, err := s.Get("../../escape"); err == nil {
		t.Fatal("path traversal allowed")
	}
}
func TestCDIAllLinuxTemplates(t *testing.T) {
	cat, err := catalog.Load("")
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, img := range cat.List() {
		if img.OS != "linux" {
			continue
		}
		count++
		items, err := CDI(img, "images", "https://artifacts.example/sha256/image.qcow2", "ceph")
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 2 || items[0]["kind"] != "DataVolume" || items[1]["kind"] != "DataSource" {
			t.Fatal("missing persistent import")
		}
	}
	if count != 6 {
		t.Fatalf("tested %d Linux templates", count)
	}
}
