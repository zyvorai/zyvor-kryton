// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package webhooks

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Guard is the SSRF policy for outbound webhook deliveries: HTTPS only,
// and no destination that resolves to a loopback, private, link-local,
// CGNAT, multicast or otherwise non-public address unless the operator
// explicitly allowlisted the host or network.
//
// It is enforced twice: ValidateURL when a subscription is created or
// changed, and again at dial time on the resolved address (DialContext),
// which defeats DNS rebinding between validation and delivery.
type Guard struct {
	allowNets  []*net.IPNet
	allowHosts map[string]bool
}

// NewGuard builds a Guard. allow entries may be CIDRs ("10.0.0.0/8"),
// single IPs ("10.1.2.3") or hostnames ("hooks.internal.example").
func NewGuard(allow []string) (*Guard, error) {
	g := &Guard{allowHosts: map[string]bool{}}
	for _, a := range allow {
		a = strings.ToLower(strings.TrimSpace(a))
		if a == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(a); err == nil {
			g.allowNets = append(g.allowNets, n)
			continue
		}
		if ip := net.ParseIP(a); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
				ip = ip.To4()
			}
			g.allowNets = append(g.allowNets, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
			continue
		}
		if strings.ContainsAny(a, "/ :") {
			return nil, fmt.Errorf("invalid webhook allowlist entry %q", a)
		}
		g.allowHosts[a] = true
	}
	return g, nil
}

var extraBlocked = mustCIDRs(
	"0.0.0.0/8", "100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15",
	"198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "64:ff9b::/96", "2001:db8::/32",
)

func mustCIDRs(s ...string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range s {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

// publicIP reports whether ip is a globally routable unicast address.
func publicIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		ip = v4
	}
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsInterfaceLocalMulticast() || ip.IsMulticast() {
		return false
	}
	for _, n := range extraBlocked {
		if n.Contains(ip) {
			return false
		}
	}
	return true
}

func (g *Guard) ipAllowed(ip net.IP) bool {
	if publicIP(ip) {
		return true
	}
	for _, n := range g.allowNets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// ErrBlocked wraps every SSRF-guard rejection.
var ErrBlocked = errors.New("webhook destination not allowed")

// ValidateURL checks scheme, userinfo and (resolved) destination.
func (g *Guard) ValidateURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return errors.New("webhook url is not a valid URL")
	}
	if u.Scheme != "https" {
		return fmt.Errorf("%w: only https URLs are accepted", ErrBlocked)
	}
	if u.User != nil {
		return fmt.Errorf("%w: credentials in the URL are not accepted", ErrBlocked)
	}
	if len(raw) > 2048 {
		return errors.New("webhook url is too long")
	}
	host := strings.ToLower(u.Hostname())
	if g.allowHosts[host] {
		return nil
	}
	if ip := net.ParseIP(host); ip != nil {
		if !g.ipAllowed(ip) {
			return fmt.Errorf("%w: %s is a private or reserved address (add it to KRYTON_WEBHOOK_ALLOW_HOSTS to permit)", ErrBlocked, ip)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("webhook host %q does not resolve", host)
	}
	for _, a := range ips {
		if !g.ipAllowed(a.IP) {
			return fmt.Errorf("%w: %s resolves to private or reserved address %s (add the host to KRYTON_WEBHOOK_ALLOW_HOSTS to permit)", ErrBlocked, host, a.IP)
		}
	}
	return nil
}

// DialContext resolves the host itself, verifies every address, then dials
// the vetted IP so the check and the connection cannot diverge.
func (g *Guard) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	d := &net.Dialer{Timeout: 5 * time.Second}
	if g.allowHosts[strings.ToLower(host)] {
		return d.DialContext(ctx, network, addr)
	}
	var ips []net.IP
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IP{ip}
	} else {
		as, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, err
		}
		for _, a := range as {
			ips = append(ips, a.IP)
		}
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("webhook host %q does not resolve", host)
	}
	for _, ip := range ips {
		if !g.ipAllowed(ip) {
			return nil, fmt.Errorf("%w: %s resolves to %s", ErrBlocked, host, ip)
		}
	}
	var last error
	for _, ip := range ips {
		c, err := d.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

// NewClient returns an HTTP client bound to the guard: no proxies, no
// redirects (a redirect could point at an internal address), bounded timeout.
func (g *Guard) NewClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	return &http.Client{
		Timeout: timeout,
		Transport: &http.Transport{
			Proxy:                 nil,
			DialContext:           g.DialContext,
			TLSHandshakeTimeout:   5 * time.Second,
			ResponseHeaderTimeout: timeout,
			MaxIdleConns:          10,
			IdleConnTimeout:       30 * time.Second,
		},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}
