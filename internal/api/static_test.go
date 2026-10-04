// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/demo"
	"github.com/zyvorai/kryton/internal/events"
	"github.com/zyvorai/kryton/internal/metrics"
)

func TestStaticSPAAssets(t *testing.T) {
	cat, err := catalog.Load("")
	if err != nil {
		t.Fatal(err)
	}
	a, err := auth.New(auth.Config{Mode: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bus, err := events.New(10, "", "", "", log)
	if err != nil {
		t.Fatal(err)
	}
	web := fstest.MapFS{
		"index.html":           {Data: []byte("<!doctype html><div id=root></div>")},
		"assets/index-abc.js":  {Data: []byte("console.log(1)")},
		"assets/index-abc.css": {Data: []byte("body{}")},
	}
	h := New(Config{Provider: demo.New(), Catalog: cat, Events: bus, Auth: a, Metrics: metrics.New(), Web: web, Projects: []string{"default"}, DefaultProject: "default", AuthMode: "disabled", Log: log}).Handler()

	cases := []struct {
		path, ctype, cache string
	}{
		{"/", "text/html", "no-store"},
		{"/assets/index-abc.js", "javascript", "immutable"},
		{"/assets/index-abc.css", "text/css", "immutable"},
		{"/machines", "text/html", "no-store"},
	}
	for _, c := range cases {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, c.path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", c.path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, c.ctype) {
			t.Errorf("%s: content-type %q, want %q", c.path, ct, c.ctype)
		}
		if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, c.cache) {
			t.Errorf("%s: cache-control %q, want %q", c.path, cc, c.cache)
		}
		if csp := w.Header().Get("Content-Security-Policy"); strings.Contains(csp, "googleapis") || !strings.Contains(csp, "script-src 'self'") {
			t.Errorf("%s: unexpected CSP %q", c.path, csp)
		}
	}
}
