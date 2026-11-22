// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEscapePath(t *testing.T) {
	if got := escapePath("/a b/c#d/../e"); got != "a%20b/c%23d/../e" {
		t.Fatalf("got %q", got)
	}
}

func TestDockurExtras(t *testing.T) {
	f := filepath.Join(t.TempDir(), "install.bat")
	if err := os.WriteFile(f, []byte("echo hi"), 0o600); err != nil {
		t.Fatal(err)
	}
	got := dockurExtras(true, f, true, true, 3390, 8007)
	if got["shared"] != true || got["oemScript"] != "echo hi" || got["jobAgent"] != true || got["publicBind"] != true || got["rdpPort"] != 3390 || got["consolePort"] != 8007 {
		t.Fatalf("extras = %v", got)
	}
	if len(dockurExtras(false, "", false, false, 0, 0)) != 0 {
		t.Fatal("empty flags must add nothing")
	}
}

func TestRawUploadSendsBodyAndAuth(t *testing.T) {
	var gotBody, gotAuth, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		gotBody, gotAuth, gotMethod = string(b), r.Header.Get("Authorization"), r.Method
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	c := client{base: srv.URL, token: "tok", project: "default", http: srv.Client()}
	if code := c.raw("PUT", "/x", strings.NewReader("data"), 4, nil); code != http.StatusCreated {
		t.Fatalf("code %d", code)
	}
	if gotBody != "data" || gotAuth != "Bearer tok" || gotMethod != "PUT" {
		t.Fatalf("server saw %q %q %q", gotMethod, gotAuth, gotBody)
	}
}
