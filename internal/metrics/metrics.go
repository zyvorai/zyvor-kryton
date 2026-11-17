// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

// Package metrics tracks HTTP request counters and latency histograms,
// per-operation counts, rate-limit rejections and pluggable gauge
// collectors (machine inventory, provider health, quotas), and exposes
// them in Prometheus text-exposition format via an http.HandlerFunc,
// without pulling in the full Prometheus client library.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
)

// DefaultBuckets are the latency histogram upper bounds in seconds.
var DefaultBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

// Sample is one labelled gauge value emitted by a Collector.
type Sample struct {
	Labels map[string]string
	Value  float64
}

// Family is a gauge/counter metric family produced at scrape time.
type Family struct {
	Name    string
	Help    string
	Type    string // "gauge" (default) or "counter"
	Samples []Sample
}

// Collector produces metric families at scrape time. visible, when
// non-nil, reports whether the scraper may see a given project (used to
// scope /metrics output for non-admin API keys).
type Collector func(visible func(project string) bool) []Family

type reqKey struct{ method, route, code string }

type hist struct {
	counts []uint64 // per-bucket (non-cumulative), len(buckets)+1 for +Inf
	sum    float64
	count  uint64
}

// Metrics accumulates process-lifetime counters for the /metrics
// endpoint. Safe for concurrent use.
type Metrics struct {
	requests    atomic.Uint64
	errors      atomic.Uint64
	rateLimited atomic.Uint64
	authFailed  atomic.Uint64
	mu          sync.RWMutex
	ops         map[string]uint64
	reqs        map[reqKey]uint64
	durations   map[reqKey]*hist
	buckets     []float64
	collectors  []Collector
	counters    map[string]map[string]uint64 // name -> encoded labels -> value
	counterHelp map[string]string
}

// New returns a zeroed Metrics ready to record against.
func New() *Metrics {
	return &Metrics{
		ops: map[string]uint64{}, reqs: map[reqKey]uint64{}, durations: map[reqKey]*hist{},
		buckets: DefaultBuckets, counters: map[string]map[string]uint64{}, counterHelp: map[string]string{},
	}
}

// Request records one completed HTTP request, tallying it as an error
// response too when errorResponse is true (status >= 400).
func (m *Metrics) Request(errorResponse bool) {
	m.requests.Add(1)
	if errorResponse {
		m.errors.Add(1)
	}
}

// ObserveRequest records the labelled request count and latency
// histogram for one finished request. route must be a low-cardinality
// template (e.g. /api/v1/machines/{id}), never a raw path.
func (m *Metrics) ObserveRequest(method, route string, status int, seconds float64) {
	k := reqKey{method, route, strconv.Itoa(status)}
	dk := reqKey{method, route, ""}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reqs[k]++
	h := m.durations[dk]
	if h == nil {
		h = &hist{counts: make([]uint64, len(m.buckets)+1)}
		m.durations[dk] = h
	}
	i := sort.SearchFloat64s(m.buckets, seconds)
	h.counts[i]++
	h.sum += seconds
	h.count++
}

// RateLimited counts one request rejected by the rate limiter.
func (m *Metrics) RateLimited() { m.rateLimited.Add(1) }

// AuthFailed counts one request rejected with 401.
func (m *Metrics) AuthFailed() { m.authFailed.Add(1) }

// Operation increments the counter for one named machine lifecycle
// operation (e.g. "create", "start", "snapshot-restore").
func (m *Metrics) Operation(name string) { m.mu.Lock(); m.ops[name]++; m.mu.Unlock() }

// Inc increments a labelled custom counter (registered lazily with help).
// Label values should be low-cardinality.
func (m *Metrics) Inc(name, help string, labels map[string]string) {
	enc := encodeLabels(labels)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.counters[name] == nil {
		m.counters[name] = map[string]uint64{}
		m.counterHelp[name] = help
	}
	m.counters[name][enc]++
}

// AddCollector registers a scrape-time gauge collector.
func (m *Metrics) AddCollector(c Collector) {
	m.mu.Lock()
	m.collectors = append(m.collectors, c)
	m.mu.Unlock()
}

// Handler serves the accumulated metrics in Prometheus text-exposition
// format with every project visible.
func (m *Metrics) Handler(w http.ResponseWriter, _ *http.Request) { m.Serve(w, nil) }

// Serve writes the exposition. visible filters per-project collector
// series; nil means everything.
func (m *Metrics) Serve(w http.ResponseWriter, visible func(string) bool) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	m.Write(w, visible)
}

// Write renders the exposition to w.
func (m *Metrics) Write(w io.Writer, visible func(string) bool) {
	_, _ = fmt.Fprintf(w, "# HELP kryton_http_requests_total HTTP requests handled.\n# TYPE kryton_http_requests_total counter\nkryton_http_requests_total %d\n", m.requests.Load())
	_, _ = fmt.Fprintf(w, "# HELP kryton_http_errors_total HTTP error responses.\n# TYPE kryton_http_errors_total counter\nkryton_http_errors_total %d\n", m.errors.Load())
	_, _ = fmt.Fprintf(w, "# HELP kryton_rate_limit_rejections_total Requests rejected by the per-caller rate limiter.\n# TYPE kryton_rate_limit_rejections_total counter\nkryton_rate_limit_rejections_total %d\n", m.rateLimited.Load())
	_, _ = fmt.Fprintf(w, "# HELP kryton_auth_failures_total Requests rejected with 401.\n# TYPE kryton_auth_failures_total counter\nkryton_auth_failures_total %d\n", m.authFailed.Load())

	m.mu.RLock()
	keys := make([]string, 0, len(m.ops))
	for k := range m.ops {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	_, _ = fmt.Fprintln(w, "# HELP kryton_operations_total Machine lifecycle operations.")
	_, _ = fmt.Fprintln(w, "# TYPE kryton_operations_total counter")
	for _, k := range keys {
		_, _ = fmt.Fprintf(w, "kryton_operations_total{operation=%q} %d\n", k, m.ops[k])
	}

	rk := make([]reqKey, 0, len(m.reqs))
	for k := range m.reqs {
		rk = append(rk, k)
	}
	sort.Slice(rk, func(i, j int) bool { return lessKey(rk[i], rk[j]) })
	_, _ = fmt.Fprintln(w, "# HELP kryton_api_requests_total API requests by method, route template and status code.")
	_, _ = fmt.Fprintln(w, "# TYPE kryton_api_requests_total counter")
	for _, k := range rk {
		_, _ = fmt.Fprintf(w, "kryton_api_requests_total{method=%q,route=%q,code=%q} %d\n", k.method, k.route, k.code, m.reqs[k])
	}

	dk := make([]reqKey, 0, len(m.durations))
	for k := range m.durations {
		dk = append(dk, k)
	}
	sort.Slice(dk, func(i, j int) bool { return lessKey(dk[i], dk[j]) })
	_, _ = fmt.Fprintln(w, "# HELP kryton_http_request_duration_seconds API request latency.")
	_, _ = fmt.Fprintln(w, "# TYPE kryton_http_request_duration_seconds histogram")
	for _, k := range dk {
		h := m.durations[k]
		var cum uint64
		for i, b := range m.buckets {
			cum += h.counts[i]
			_, _ = fmt.Fprintf(w, "kryton_http_request_duration_seconds_bucket{method=%q,route=%q,le=%q} %d\n", k.method, k.route, formatFloat(b), cum)
		}
		cum += h.counts[len(m.buckets)]
		_, _ = fmt.Fprintf(w, "kryton_http_request_duration_seconds_bucket{method=%q,route=%q,le=\"+Inf\"} %d\n", k.method, k.route, cum)
		_, _ = fmt.Fprintf(w, "kryton_http_request_duration_seconds_sum{method=%q,route=%q} %s\n", k.method, k.route, formatFloat(h.sum))
		_, _ = fmt.Fprintf(w, "kryton_http_request_duration_seconds_count{method=%q,route=%q} %d\n", k.method, k.route, h.count)
	}

	names := make([]string, 0, len(m.counters))
	for n := range m.counters {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		_, _ = fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n", n, m.counterHelp[n], n)
		lk := make([]string, 0, len(m.counters[n]))
		for l := range m.counters[n] {
			lk = append(lk, l)
		}
		sort.Strings(lk)
		for _, l := range lk {
			_, _ = fmt.Fprintf(w, "%s%s %d\n", n, l, m.counters[n][l])
		}
	}
	collectors := append([]Collector(nil), m.collectors...)
	m.mu.RUnlock()

	for _, c := range collectors {
		for _, f := range c(visible) {
			writeFamily(w, f)
		}
	}
}

func writeFamily(w io.Writer, f Family) {
	typ := f.Type
	if typ == "" {
		typ = "gauge"
	}
	_, _ = fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s %s\n", f.Name, f.Help, f.Name, typ)
	sort.SliceStable(f.Samples, func(i, j int) bool { return encodeLabels(f.Samples[i].Labels) < encodeLabels(f.Samples[j].Labels) })
	for _, s := range f.Samples {
		_, _ = fmt.Fprintf(w, "%s%s %s\n", f.Name, encodeLabels(s.Labels), formatFloat(s.Value))
	}
}

func lessKey(a, b reqKey) bool {
	if a.route != b.route {
		return a.route < b.route
	}
	if a.method != b.method {
		return a.method < b.method
	}
	return a.code < b.code
}

func formatFloat(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }

func encodeLabels(l map[string]string) string {
	if len(l) == 0 {
		return ""
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteByte('{')
	for i, k := range keys {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(strconv.Quote(l[k]))
	}
	b.WriteByte('}')
	return b.String()
}
