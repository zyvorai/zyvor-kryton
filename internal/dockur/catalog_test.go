// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package dockur

import (
	"testing"

	"github.com/zyvorai/kryton/internal/catalog"
)

// Codes from the dockur/windows README "VERSION" table (verified 2026-11).
func TestCatalogDockurCodes(t *testing.T) {
	cat, err := catalog.Load("")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"windows-11-pro": "11", "windows-11-enterprise": "11e", "windows-11-ltsc": "11l",
		"windows-10-pro": "10", "windows-10-enterprise": "10e", "windows-10-ltsc": "10l",
		"windows-server-2025": "2025", "windows-server-2022": "2022", "windows-server-2019": "2019",
		"windows-server-2016": "2016", "windows-server-2012": "2012",
		"windows-tiny11": "tiny11", "windows-tiny11-core": "core11", "windows-tiny10": "tiny10",
	}
	for imgID, code := range want {
		img, ok := cat.Get(imgID)
		if !ok {
			t.Fatalf("catalog missing %s", imgID)
		}
		if img.DockurVersion != code {
			t.Errorf("%s: catalog code %q, want %q", imgID, img.DockurVersion, code)
		}
		if defaultVersions[imgID] != code {
			t.Errorf("%s: provider fallback %q, want %q", imgID, defaultVersions[imgID], code)
		}
		if !img.SupportsProvider("dockur") || img.Architecture != "amd64" {
			t.Errorf("%s: should be amd64 and dockur-capable", imgID)
		}
	}
	if img, _ := cat.Get("windows-11-enterprise"); img.Version != "25H2" {
		t.Errorf("Windows 11 Enterprise version = %q, want 25H2", img.Version)
	}
}
