// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/zyvorai/kryton/internal/auth"
)

const (
	consoleTicketParam  = "console_ticket"
	consoleTicketCookie = "kryton_console"
	consoleTicketTTL    = 10 * time.Minute
)

// consoleTickets mints and verifies short-lived, HMAC-signed tickets that
// let a browser iframe or websocket — which cannot send an Authorization
// header — open one machine's console with viewer rights. The signing key
// is per-process, so a krytond restart invalidates every ticket.
type consoleTickets struct {
	key [32]byte
	now func() time.Time
}

func newConsoleTickets() *consoleTickets {
	t := &consoleTickets{now: time.Now}
	if _, err := rand.Read(t.key[:]); err != nil {
		panic("console tickets: " + err.Error())
	}
	return t
}

func (t *consoleTickets) issue(p auth.Principal, project, machineID string) (string, time.Time) {
	exp := t.now().Add(consoleTicketTTL).Truncate(time.Second)
	fields := []string{"v1", strconv.FormatInt(exp.Unix(), 10), project, machineID, p.Name}
	for i := range fields {
		fields[i] = url.QueryEscape(fields[i])
	}
	payload := base64.RawURLEncoding.EncodeToString([]byte(strings.Join(fields, "|")))
	return payload + "." + t.sign(payload), exp
}

func (t *consoleTickets) verify(ticket, machineID string) (auth.Principal, error) {
	payload, sig, ok := strings.Cut(ticket, ".")
	if !ok || subtle.ConstantTimeCompare([]byte(sig), []byte(t.sign(payload))) != 1 {
		return auth.Principal{}, errors.New("invalid console ticket")
	}
	raw, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return auth.Principal{}, errors.New("invalid console ticket")
	}
	fields := strings.Split(string(raw), "|")
	if len(fields) != 5 || fields[0] != "v1" {
		return auth.Principal{}, errors.New("invalid console ticket")
	}
	for i := range fields {
		if fields[i], err = url.QueryUnescape(fields[i]); err != nil {
			return auth.Principal{}, errors.New("invalid console ticket")
		}
	}
	exp, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || t.now().Unix() >= exp {
		return auth.Principal{}, errors.New("console ticket expired")
	}
	if fields[3] != machineID {
		return auth.Principal{}, errors.New("console ticket is for another machine")
	}
	return auth.Principal{Name: fields[4] + " (console)", Role: auth.Viewer, Projects: []string{fields[2]}}, nil
}

func (t *consoleTickets) sign(payload string) string {
	m := hmac.New(sha256.New, t.key[:])
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

// consolePath returns the machine ID when path is one of the read-only
// console endpoints a ticket may unlock: /console, /console/..., or /vnc.
func consolePath(path string) (string, bool) {
	rest, ok := strings.CutPrefix(path, "/api/v1/machines/")
	if !ok {
		return "", false
	}
	id, sub, ok := strings.Cut(rest, "/")
	if !ok || id == "" {
		return "", false
	}
	if sub == "vnc" || sub == "console" || strings.HasPrefix(sub, "console/") {
		return id, true
	}
	return "", false
}

// consoleTicketAuth serves console requests that carry a valid ticket
// (query parameter or cookie) and no Authorization header via ticketed,
// with a viewer Principal scoped to the ticket's project; every other
// request goes to authed unchanged.
func (s *Server) consoleTicketAuth(authed, ticketed http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" || (r.Method != http.MethodGet && r.Method != http.MethodHead) {
			authed.ServeHTTP(w, r)
			return
		}
		machineID, ok := consolePath(r.URL.Path)
		if !ok {
			authed.ServeHTTP(w, r)
			return
		}
		query := r.URL.Query().Get(consoleTicketParam)
		var cookie string
		if c, err := r.Cookie(consoleTicketCookie); err == nil {
			cookie = c.Value
		}
		if query == "" && cookie == "" {
			authed.ServeHTTP(w, r)
			return
		}
		// A reloaded iframe keeps its original (possibly expired) query
		// ticket, while the UI keeps the cookie fresh — so fall back to it.
		err := errors.New("missing console ticket")
		for i, ticket := range []string{query, cookie} {
			if ticket == "" {
				continue
			}
			var p auth.Principal
			if p, err = s.consoleTickets.verify(ticket, machineID); err != nil {
				continue
			}
			if i == 0 && ticket != cookie {
				setConsoleCookie(w, r, machineID, ticket)
			}
			ticketed.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
			return
		}
		w.Header().Set("WWW-Authenticate", `Bearer realm="kryton"`)
		http.Error(w, "unauthorized: "+err.Error(), http.StatusUnauthorized)
	})
}

// setConsoleCookie stores ticket in a cookie scoped to one machine's API
// path, covering the sub-resources and websockets a console page opens
// without the query parameter.
func setConsoleCookie(w http.ResponseWriter, r *http.Request, machineID, ticket string) {
	// #nosec G124 -- HttpOnly+SameSite=Strict always; Secure only when served over TLS so plain-HTTP lab installs work
	http.SetCookie(w, &http.Cookie{
		Name:     consoleTicketCookie,
		Value:    ticket,
		Path:     "/api/v1/machines/" + machineID + "/",
		MaxAge:   int(consoleTicketTTL / time.Second),
		HttpOnly: true,
		Secure:   r.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *Server) machineConsoleTicket(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	machineID := r.PathValue("id")
	if _, err := s.p.Get(r.Context(), project, machineID); err != nil {
		s.writeErr(w, r, err)
		return
	}
	ticket, exp := s.consoleTickets.issue(auth.FromContext(r.Context()), project, machineID)
	setConsoleCookie(w, r, machineID, ticket)
	jsonResponse(w, http.StatusOK, map[string]any{"ticket": ticket, "expiresAt": exp.UTC()})
}
