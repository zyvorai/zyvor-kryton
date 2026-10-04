// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/zyvorai/kryton/internal/auth"
)

// The demo provider does not implement provider.ConsoleResolver, so
// console/vnc endpoints against it exercise the "unsupported provider"
// branch — the only console behavior reachable without a real
// dockur/kubevirt backend.

func TestMachineConsoleUnsupportedForDemoProviderJSON(t *testing.T) {
	h, _ := testServer(t)
	body := map[string]any{"project": "default", "name": "win-01", "image": "windows-server-2025", "compute": map[string]any{"cpu": 4, "memoryMiB": 8192}, "disk": map[string]any{"sizeGiB": 80}}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/machines", bytes.NewReader(b)))
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/machines/"+m.ID+"/console?project=default", nil)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 for demo provider console, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMachineConsoleUnsupportedForDemoProviderHTML(t *testing.T) {
	h, _ := testServer(t)
	body := map[string]any{"project": "default", "name": "win-02", "image": "windows-server-2025", "compute": map[string]any{"cpu": 4, "memoryMiB": 8192}, "disk": map[string]any{"sizeGiB": 80}}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/machines", bytes.NewReader(b)))
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/machines/"+m.ID+"/console?project=default", nil)
	r.Header.Set("Accept", "text/html")
	h.ServeHTTP(w, r)
	// writeConsoleHTML is only reached once ConsoleTarget succeeds; the
	// demo provider fails the ConsoleResolver type assertion before that,
	// so even with an HTML Accept header this stays a JSON 501, not a
	// text/html error page — confirms the resolver-support check runs first.
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMachineVNCUnsupportedWithoutKubeClient(t *testing.T) {
	h, _ := testServer(t)
	body := map[string]any{"project": "default", "name": "win-03", "image": "windows-server-2025", "compute": map[string]any{"cpu": 4, "memoryMiB": 8192}, "disk": map[string]any{"sizeGiB": 80}}
	b, _ := json.Marshal(body)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/machines", bytes.NewReader(b)))
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil {
		t.Fatal(err)
	}

	w = httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/machines/"+m.ID+"/vnc?project=default", nil)
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501 without a kube client, got %d: %s", w.Code, w.Body.String())
	}
}

func TestMachineConsoleRequiresViewerRole(t *testing.T) {
	h, _, _ := testServerAPIKeyRoles(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/api/v1/machines/does-not-matter/console?project=default", nil)
	// No Authorization header at all in apikey mode: fails auth entirely (401),
	// distinct from the 403 a wrong-project/role principal would get.
	h.ServeHTTP(w, r)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credentials, got %d: %s", w.Code, w.Body.String())
	}
}

func createMachineAs(t *testing.T, h http.Handler, token, name string) string {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"project": "default", "name": name, "image": "windows-server-2025", "compute": map[string]any{"cpu": 4, "memoryMiB": 8192}, "disk": map[string]any{"sizeGiB": 80}})
	r := httptest.NewRequest(http.MethodPost, "/api/v1/machines", bytes.NewReader(b))
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var m struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &m); err != nil || m.ID == "" {
		t.Fatalf("create machine: %d %s", w.Code, w.Body.String())
	}
	return m.ID
}

func mintConsoleTicket(t *testing.T, h http.Handler, token, id string) string {
	t.Helper()
	r := httptest.NewRequest(http.MethodPost, "/api/v1/machines/"+id+"/console-ticket?project=default", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	var out struct {
		Ticket string `json:"ticket"`
	}
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &out) != nil || out.Ticket == "" {
		t.Fatalf("mint ticket: %d %s", w.Code, w.Body.String())
	}
	return out.Ticket
}

// Ticketed requests that pass auth reach the demo provider's 501
// "unsupported console" branch; rejected ones stop at 401.
func TestConsoleTicketUnlocksOnlyThatMachinesConsole(t *testing.T) {
	h, viewer, admin := testServerAPIKeyRoles(t)
	id := createMachineAs(t, h, admin, "win-t1")
	other := createMachineAs(t, h, admin, "win-t2")
	ticket := mintConsoleTicket(t, h, viewer, id)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/v1/machines/"+id+"/console/?project=default&format=html&console_ticket="+url.QueryEscape(ticket), nil))
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("query ticket: expected 501, got %d: %s", w.Code, w.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == consoleTicketCookie {
			cookie = c
		}
	}
	if cookie == nil || !cookie.HttpOnly || cookie.Path != "/api/v1/machines/"+id+"/" || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("expected scoped HttpOnly console cookie, got %+v", cookie)
	}

	r := httptest.NewRequest(http.MethodGet, "/api/v1/machines/"+id+"/vnc?project=default", nil)
	r.AddCookie(cookie)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("cookie ticket on vnc: expected 501, got %d: %s", w.Code, w.Body.String())
	}

	for name, req := range map[string]*http.Request{
		"other machine":   httptest.NewRequest(http.MethodGet, "/api/v1/machines/"+other+"/console?project=default&console_ticket="+url.QueryEscape(ticket), nil),
		"non-console":     httptest.NewRequest(http.MethodGet, "/api/v1/machines/"+id+"?project=default&console_ticket="+url.QueryEscape(ticket), nil),
		"mutating method": httptest.NewRequest(http.MethodPost, "/api/v1/machines/"+id+"/console?project=default&console_ticket="+url.QueryEscape(ticket), nil),
		"tampered":        httptest.NewRequest(http.MethodGet, "/api/v1/machines/"+id+"/console?project=default&console_ticket="+url.QueryEscape(ticket+"x"), nil),
	} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: expected 401, got %d: %s", name, w.Code, w.Body.String())
		}
	}
}

func TestConsoleTicketRequiresAuthAndExistingMachine(t *testing.T) {
	h, viewer, _ := testServerAPIKeyRoles(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/v1/machines/x/console-ticket?project=default", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without credentials, got %d", w.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/v1/machines/missing/console-ticket?project=default", nil)
	r.Header.Set("Authorization", "Bearer "+viewer)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for unknown machine, got %d: %s", w.Code, w.Body.String())
	}
}

func TestConsoleTicketExpiresAndScopesPrincipal(t *testing.T) {
	ct := newConsoleTickets()
	now := time.Unix(1_800_000_000, 0)
	ct.now = func() time.Time { return now }
	ticket, _ := ct.issue(auth.Principal{Name: "alice|x"}, "default", "m1")
	p, err := ct.verify(ticket, "m1")
	if err != nil {
		t.Fatal(err)
	}
	if p.Role != auth.Viewer || len(p.Projects) != 1 || p.Projects[0] != "default" || p.Name != "alice|x (console)" {
		t.Fatalf("unexpected principal %+v", p)
	}
	now = now.Add(consoleTicketTTL)
	if _, err := ct.verify(ticket, "m1"); err == nil {
		t.Fatal("expected expired ticket to be rejected")
	}
	if _, err := newConsoleTickets().verify(ticket, "m1"); err == nil {
		t.Fatal("expected ticket from another signing key to be rejected")
	}
}

func TestWriteConsoleHTMLEscapesMachineID(t *testing.T) {
	w := httptest.NewRecorder()
	(&Server{}).writeConsoleHTML(w, `<img src=x onerror=alert(1)>`, "default", "boom")
	if strings.Contains(w.Body.String(), "<img") {
		t.Fatalf("machine ID was not escaped: %s", w.Body.String())
	}
}
