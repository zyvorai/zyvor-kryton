// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package webhooks

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zyvorai/kryton/internal/events"
)

func TestGuardValidateURL(t *testing.T) {
	g, _ := NewGuard([]string{"10.1.0.0/16", "hooks.internal.example", "192.168.5.5"})
	cases := []struct {
		url string
		ok  bool
	}{
		{"http://93.184.216.34/hook", false},
		{"https://93.184.216.34/hook", true},
		{"https://127.0.0.1/hook", false},
		{"https://[::1]/hook", false},
		{"https://10.0.0.5/hook", false},
		{"https://10.1.2.3/hook", true},
		{"https://172.16.0.1/", false},
		{"https://192.168.1.1/", false},
		{"https://192.168.5.5/", true},
		{"https://169.254.169.254/latest/meta-data", false},
		{"https://100.64.0.1/", false},
		{"https://0.0.0.0/", false},
		{"https://[::ffff:10.0.0.1]/", false},
		{"https://[fd00::1]/", false},
		{"https://localhost/hook", false},
		{"https://user:pw@93.184.216.34/", false},
		{"ftp://93.184.216.34/", false},
		{"https://hooks.internal.example/x", true},
		{"not a url", false},
	}
	for _, c := range cases {
		err := g.ValidateURL(c.url)
		if (err == nil) != c.ok {
			t.Errorf("%s: err=%v want ok=%v", c.url, err, c.ok)
		}
	}
	if _, err := NewGuard([]string{"bad/entry/x"}); err == nil {
		t.Error("bad allowlist entry accepted")
	}
}

func TestGuardDialBlocksPrivate(t *testing.T) {
	g, _ := NewGuard(nil)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	_, err = g.DialContext(context.Background(), "tcp", ln.Addr().String())
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("expected ErrBlocked, got %v", err)
	}
	_, err = g.DialContext(context.Background(), "tcp", "localhost:"+strconv.Itoa(ln.Addr().(*net.TCPAddr).Port))
	if !errors.Is(err, ErrBlocked) {
		t.Fatalf("localhost should be blocked at dial time, got %v", err)
	}
	allow, _ := NewGuard([]string{"127.0.0.0/8"})
	c, err := allow.DialContext(context.Background(), "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("allowlisted dial failed: %v", err)
	}
	_ = c.Close()
}

func TestMatches(t *testing.T) {
	if !Matches(nil, "io.kryton.machine.created") || !Matches([]string{"*"}, "x") {
		t.Fatal("empty/* should match all")
	}
	if !Matches([]string{"io.kryton.machine.*"}, "io.kryton.machine.created") {
		t.Fatal("glob should match")
	}
	if Matches([]string{"io.kryton.snapshot.*"}, "io.kryton.machine.created") {
		t.Fatal("glob should not match")
	}
}

func fast() Config {
	return Config{Backoff: []time.Duration{10 * time.Millisecond, 10 * time.Millisecond}, Tick: 5 * time.Millisecond, Workers: 2}
}

func newMgr(t *testing.T, srv *httptest.Server, cfg Config) *Manager {
	t.Helper()
	g, _ := NewGuard([]string{"127.0.0.0/8"})
	cfg.Guard = g
	if srv != nil {
		cfg.Client = srv.Client()
	}
	m, err := NewManager(cfg)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Start(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return m
}

func ev(project, typ string) events.Event {
	return events.Event{SpecVersion: "1.0", ID: "ev-" + typ, Source: "kryton://control-plane", Type: typ, Time: time.Now().UTC(), DataContentType: "application/json", Data: map[string]any{"project": project}}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestDeliverySignedAndProjectScoped(t *testing.T) {
	var mu sync.Mutex
	var got []*http.Request
	var bodies [][]byte
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		got = append(got, r)
		bodies = append(bodies, b)
		mu.Unlock()
		w.WriteHeader(204)
	}))
	defer srv.Close()
	m := newMgr(t, srv, fast())
	sub, err := m.Create(Subscription{Project: "lab", URL: srv.URL, EventTypes: []string{"io.kryton.machine.*"}})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Secret == "" {
		t.Fatal("create must return the secret once")
	}
	if g, _ := m.Get(sub.ID); g.Secret != "" {
		t.Fatal("secret leaked on read")
	}
	m.Dispatch(ev("other", "io.kryton.machine.created")) // wrong project
	m.Dispatch(ev("lab", "io.kryton.snapshot.created"))  // wrong type
	m.Dispatch(ev("lab", "io.kryton.machine.created"))   // delivered
	waitFor(t, "delivery", func() bool { mu.Lock(); defer mu.Unlock(); return len(got) >= 1 })
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 {
		t.Fatalf("expected exactly 1 delivery, got %d", len(got))
	}
	r := got[0]
	ts, _ := strconv.ParseInt(r.Header.Get("X-Kryton-Timestamp"), 10, 64)
	if want := Sign(sub.Secret, ts, bodies[0]); r.Header.Get("X-Kryton-Signature") != want {
		t.Fatalf("bad signature %q want %q", r.Header.Get("X-Kryton-Signature"), want)
	}
	if r.Header.Get("X-Kryton-Event-Type") != "io.kryton.machine.created" || r.Header.Get("X-Kryton-Attempt") != "1" {
		t.Fatalf("headers: %v", r.Header)
	}
	list, _, _ := m.ListDeliveries(DeliveryFilter{Project: "lab"})
	if len(list) != 1 || list[0].Status != StatusSucceeded || list[0].Payload != nil {
		t.Fatalf("delivery log: %+v", list)
	}
}

func TestRetriesThenDeadLetterThenRedeliver(t *testing.T) {
	var healthy atomic.Bool
	var hits atomic.Int32
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if healthy.Load() {
			w.WriteHeader(200)
			return
		}
		w.WriteHeader(500)
	}))
	defer srv.Close()
	var outcomes sync.Map
	cfg := fast()
	cfg.OnAttempt = func(_, outcome string) {
		v, _ := outcomes.LoadOrStore(outcome, new(atomic.Int32))
		v.(*atomic.Int32).Add(1)
	}
	m := newMgr(t, srv, cfg)
	sub, _ := m.Create(Subscription{Project: "lab", URL: srv.URL})
	m.Dispatch(ev("lab", "io.kryton.machine.created"))
	waitFor(t, "dead letter", func() bool {
		l, _, _ := m.ListDeliveries(DeliveryFilter{Status: StatusDead})
		return len(l) == 1
	})
	if hits.Load() != 3 { // 1 try + 2 retries
		t.Fatalf("attempts=%d want 3", hits.Load())
	}
	dead, _, _ := m.ListDeliveries(DeliveryFilter{Status: StatusDead})
	d, _ := m.GetDelivery(dead[0].ID)
	if d.Attempts != 3 || d.LastStatusCode != 500 || len(d.AttemptLog) != 3 || len(d.Payload) == 0 {
		t.Fatalf("dead delivery detail: %+v", d)
	}
	if m.DeadLetterCount()["lab"] != 1 {
		t.Fatal("dead letter count")
	}
	healthy.Store(true)
	if _, err := m.Redeliver(d.ID); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "redelivery", func() bool {
		x, _ := m.GetDelivery(d.ID)
		return x.Status == StatusSucceeded
	})
	if v, ok := outcomes.Load("dead"); !ok || v.(*atomic.Int32).Load() != 1 {
		t.Fatal("OnAttempt dead outcome not reported")
	}
	// Deleting the subscription kills its pending deliveries and blocks redelivery.
	_ = m.Delete(sub.ID)
	if _, err := m.Redeliver(d.ID); err == nil {
		t.Fatal("redeliver for deleted subscription should fail")
	}
}

func TestSubscriptionValidationAndLimits(t *testing.T) {
	m, _ := NewManager(Config{})
	if _, err := m.Create(Subscription{Project: "lab", URL: "http://example.com/x"}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("http url: %v", err)
	}
	if _, err := m.Create(Subscription{Project: "lab", URL: "https://10.0.0.1/x"}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("private url: %v", err)
	}
	if _, err := m.Create(Subscription{Project: "lab", URL: "https://93.184.216.34/x", EventTypes: []string{"["}}); err == nil {
		t.Fatal("bad pattern accepted")
	}
	for i := 0; i < MaxSubscriptionsPerProject; i++ {
		if _, err := m.Create(Subscription{Project: "lab", URL: "https://93.184.216.34/x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := m.Create(Subscription{Project: "lab", URL: "https://93.184.216.34/x"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("limit: %v", err)
	}
	s := m.List("lab", nil)[0]
	off := false
	if _, err := m.Update(s.ID, Patch{Enabled: &off}); err != nil {
		t.Fatal(err)
	}
	bad := "http://x.example"
	if _, err := m.Update(s.ID, Patch{URL: &bad}); !errors.Is(err, ErrBlocked) {
		t.Fatalf("update must revalidate: %v", err)
	}
	if _, err := m.Update("nope", Patch{}); !errors.Is(err, ErrNotFound) {
		t.Fatal("update unknown")
	}
}

func TestPersistenceAndEviction(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "webhooks.json")
	g, _ := NewGuard(nil)
	m, err := NewManager(Config{File: file, Guard: g, MaxDeliveries: 10})
	if err != nil {
		t.Fatal(err)
	}
	sub, err := m.Create(Subscription{Project: "lab", URL: "https://93.184.216.34/x"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 15; i++ {
		m.Dispatch(ev("lab", "io.kryton.machine.created"))
	}
	m.mu.Lock()
	for _, d := range m.deliv {
		d.Status = StatusSucceeded
	}
	m.dirty = true
	m.evictLocked()
	n := len(m.order)
	m.mu.Unlock()
	if n > 10 {
		t.Fatalf("log not bounded: %d", n)
	}
	m.Dispatch(ev("lab", "io.kryton.machine.deleted")) // pending delivery must survive eviction
	m.flushDeliveries()

	m2, err := NewManager(Config{File: file, Guard: g})
	if err != nil {
		t.Fatal(err)
	}
	if got, err := m2.Get(sub.ID); err != nil || got.URL != sub.URL {
		t.Fatalf("subscription not persisted: %v %+v", err, got)
	}
	l, _, _ := m2.ListDeliveries(DeliveryFilter{Status: StatusPending})
	if len(l) != 1 {
		t.Fatalf("pending delivery not persisted: %d", len(l))
	}
	if _, _, err := m2.ListDeliveries(DeliveryFilter{Cursor: "***"}); err == nil {
		t.Fatal("bad cursor accepted")
	}
}

func TestDeliveryPagination(t *testing.T) {
	m, _ := NewManager(Config{})
	_, _ = m.Create(Subscription{Project: "lab", URL: "https://93.184.216.34/x"})
	for i := 0; i < 5; i++ {
		m.Dispatch(ev("lab", "io.kryton.machine.created"))
	}
	var all int
	cursor := ""
	for i := 0; i < 5; i++ {
		page, next, err := m.ListDeliveries(DeliveryFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatal(err)
		}
		all += len(page)
		if next == "" {
			break
		}
		cursor = next
	}
	if all != 5 {
		t.Fatalf("paged %d want 5", all)
	}
}
