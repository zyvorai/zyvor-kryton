// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

// Package auth implements krytond's authentication and role model:
// disabled, apikey (hashed keys loaded from a JSON file), and proxy
// (trusting X-Kryton-User/-Role/-Projects headers from a reverse proxy).
// It resolves each request to a Principal scoped to a Role (viewer,
// operator, admin) and a set of allowed projects.
package auth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

// Role is a Kryton access level; Can and rank treat these as a strict
// hierarchy Viewer < Operator < Admin, so holding Admin satisfies any
// lower requirement too.
type Role string

const (
	Viewer   Role = "viewer"
	Operator Role = "operator"
	Admin    Role = "admin"
)

// Principal is the caller identity resolved from a request by
// Authenticator.Middleware, retrievable in a handler via FromContext.
type Principal struct {
	Name string `json:"name"`
	// Role applies to every project listed in Projects.
	Role     Role     `json:"role"`
	Projects []string `json:"projects"` // "*" grants every project
	// Grants gives a specific Role on individual projects, in addition to
	// Role/Projects; the highest matching role wins. It lets one key be
	// viewer on "prod" and operator on "lab".
	Grants map[string]Role `json:"grants,omitempty"`
}

// Config selects and configures an auth Mode ("disabled", "apikey", or "proxy") for New.
type Config struct {
	Mode        string
	APIKeysFile string
	TrustProxy  bool
	// ProxySecretFile holds the shared secret the reverse proxy must
	// present in X-Kryton-Proxy-Secret; required when Mode is "proxy".
	ProxySecretFile string
}

type keyRecord struct {
	Name   string `json:"name"`
	SHA256 string `json:"sha256,omitempty"`
	// Role is optional for backward compatibility: keys written before
	// per-project grants existed always carry it, and a record that omits
	// it (but lists projects) is treated as admin.
	Role     Role            `json:"role,omitempty"`
	Projects []string        `json:"projects,omitempty"`
	Grants   map[string]Role `json:"grants,omitempty"`
	hash     [32]byte
}

type keyFile struct {
	Keys []keyRecord `json:"keys"`
}

// Authenticator implements one auth Mode for the lifetime of a krytond
// process; build it once with New and install it via Middleware. It is
// safe for concurrent use — all state is read-only after New returns.
type Authenticator struct {
	mode            string
	trustProxy      bool
	keys            []keyRecord
	proxySecretHash [32]byte
	hasProxySecret  bool
}

type contextKey struct{}

// New builds an Authenticator for cfg.Mode. For "apikey" it loads and
// validates cfg.APIKeysFile (every key needs a sha256 digest — see
// HashToken); for "proxy" it loads cfg.ProxySecretFile. Either read
// failure, or an apikey file with zero valid keys, is returned as an error.
func New(cfg Config) (*Authenticator, error) {
	a := &Authenticator{mode: cfg.Mode, trustProxy: cfg.TrustProxy}
	if cfg.Mode == "proxy" {
		b, err := os.ReadFile(cfg.ProxySecretFile)
		if err != nil {
			return nil, fmt.Errorf("read proxy secret file: %w", err)
		}
		secret := strings.TrimSpace(string(b))
		if secret == "" {
			return nil, errors.New("proxy secret file is empty")
		}
		a.proxySecretHash = sha256.Sum256([]byte(secret))
		a.hasProxySecret = true
		return a, nil
	}
	if cfg.Mode != "apikey" {
		return a, nil
	}
	b, err := os.ReadFile(cfg.APIKeysFile)
	if err != nil {
		return nil, fmt.Errorf("read api keys file: %w", err)
	}
	var f keyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("parse api keys file: %w", err)
	}
	if len(f.Keys) == 0 {
		return nil, errors.New("api keys file contains no keys")
	}
	for i := range f.Keys {
		k := &f.Keys[i]
		if k.Role == "" && len(k.Projects) > 0 {
			k.Role = Admin // legacy/unscoped-role keys keep full access
		}
		if k.Name == "" || len(k.Projects)+len(k.Grants) == 0 {
			return nil, fmt.Errorf("invalid api key entry %d", i)
		}
		if len(k.Projects) > 0 && !validRole(k.Role) {
			return nil, fmt.Errorf("api key %q has invalid role %q", k.Name, k.Role)
		}
		for proj, gr := range k.Grants {
			if proj == "" || !validRole(gr) {
				return nil, fmt.Errorf("api key %q has an invalid grant %q=%q", k.Name, proj, gr)
			}
		}
		if k.SHA256 == "" {
			return nil, fmt.Errorf("api key %q requires a sha256 digest", k.Name)
		}
		raw, err := hex.DecodeString(k.SHA256)
		if err != nil || len(raw) != sha256.Size {
			return nil, fmt.Errorf("api key %q has invalid sha256", k.Name)
		}
		copy(k.hash[:], raw)
	}
	a.keys = f.Keys
	return a, nil
}

// HashToken returns the hex-encoded SHA-256 digest of token — the value
// that belongs in an API keys file's "sha256" field. Kryton never stores
// or logs the raw token, only this digest.
func HashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}

// Middleware authenticates every request before it reaches next,
// rejecting with 401 on failure and otherwise storing the resolved
// Principal in the request context for FromContext to retrieve.
func (a *Authenticator) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, err := a.authenticate(r)
		if err != nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="kryton"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, p)))
	})
}

// Authenticate resolves the Principal for r without touching the response;
// it is for handlers (such as /metrics) that gate themselves.
func (a *Authenticator) Authenticate(r *http.Request) (Principal, error) { return a.authenticate(r) }

func (a *Authenticator) authenticate(r *http.Request) (Principal, error) {
	switch a.mode {
	case "disabled":
		return Principal{Name: "local-demo", Role: Admin, Projects: []string{"*"}}, nil
	case "proxy":
		if !a.trustProxy || !a.hasProxySecret {
			return Principal{}, errors.New("proxy is not trusted")
		}
		presented := sha256.Sum256([]byte(strings.TrimSpace(r.Header.Get("X-Kryton-Proxy-Secret"))))
		if subtle.ConstantTimeCompare(presented[:], a.proxySecretHash[:]) != 1 {
			return Principal{}, errors.New("invalid proxy secret")
		}
		name := strings.TrimSpace(r.Header.Get("X-Kryton-User"))
		role := Role(strings.ToLower(strings.TrimSpace(r.Header.Get("X-Kryton-Role"))))
		projects := splitCSV(r.Header.Get("X-Kryton-Projects"))
		grants, gerr := parseGrants(r.Header.Get("X-Kryton-Grants"))
		if gerr != nil {
			return Principal{}, gerr
		}
		if name == "" || (len(projects) > 0 && !validRole(role)) || len(projects)+len(grants) == 0 {
			return Principal{}, errors.New("missing trusted proxy identity headers")
		}
		return Principal{Name: name, Role: role, Projects: projects, Grants: grants}, nil
	case "apikey":
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			return Principal{}, errors.New("missing bearer token")
		}
		tokenHash := sha256.Sum256([]byte(strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))))
		for _, k := range a.keys {
			if subtle.ConstantTimeCompare(tokenHash[:], k.hash[:]) == 1 {
				return Principal{Name: k.Name, Role: k.Role, Projects: append([]string(nil), k.Projects...), Grants: copyGrants(k.Grants)}, nil
			}
		}
		return Principal{}, errors.New("invalid bearer token")
	default:
		return Principal{}, errors.New("unsupported auth mode")
	}
}

// WithPrincipal returns a copy of ctx carrying p, for callers that
// authenticate a request by other means than Middleware (such as a
// scoped console ticket).
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, contextKey{}, p)
}

// FromContext retrieves the Principal Middleware attached to ctx,
// or the zero Principal (no role, no projects) if none was set.
func FromContext(ctx context.Context) Principal {
	if p, ok := ctx.Value(contextKey{}).(Principal); ok {
		return p
	}
	return Principal{}
}

// Can reports whether p holds at least the required Role on project:
// either through Role + Projects (directly or via a "*" wildcard) or
// through a per-project entry in Grants.
func Can(p Principal, project string, required Role) bool {
	return rank(RoleFor(p, project)) >= rank(required) && rank(required) > 0
}

// RoleFor returns the highest Role p holds on project, or "" if none.
func RoleFor(p Principal, project string) Role {
	best := Role("")
	for _, allowed := range p.Projects {
		if (allowed == "*" || allowed == project) && rank(p.Role) > rank(best) {
			best = p.Role
		}
	}
	if g, ok := p.Grants[project]; ok && rank(g) > rank(best) {
		best = g
	}
	return best
}

// IsGlobalAdmin reports whether p is admin on every project ("*").
func IsGlobalAdmin(p Principal) bool { return Can(p, "*", Admin) }

func copyGrants(in map[string]Role) map[string]Role {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]Role, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// parseGrants parses "proj=role,proj2=role" (the X-Kryton-Grants header).
func parseGrants(v string) (map[string]Role, error) {
	out := map[string]Role{}
	for _, part := range splitCSV(v) {
		proj, role, ok := strings.Cut(part, "=")
		r := Role(strings.ToLower(strings.TrimSpace(role)))
		if !ok || strings.TrimSpace(proj) == "" || !validRole(r) {
			return nil, errors.New("invalid X-Kryton-Grants header")
		}
		out[strings.TrimSpace(proj)] = r
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// FilterProjects returns the subset of configured that p can at least
// view, preserving order — the list GET /api/v1/projects returns.
func FilterProjects(p Principal, configured []string) []string {
	var out []string
	for _, project := range configured {
		if Can(p, project, Viewer) {
			out = append(out, project)
		}
	}
	return out
}

func validRole(r Role) bool { return r == Viewer || r == Operator || r == Admin }
func rank(r Role) int {
	switch r {
	case Admin:
		return 3
	case Operator:
		return 2
	case Viewer:
		return 1
	default:
		return 0
	}
}

func splitCSV(v string) []string {
	var out []string
	for _, x := range strings.Split(v, ",") {
		x = strings.TrimSpace(x)
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}
