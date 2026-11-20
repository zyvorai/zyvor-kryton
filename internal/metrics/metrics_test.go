// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package metrics

import (
	"strings"
	"testing"
)

func render(m *Metrics, visible func(string) bool) string {
	var b strings.Builder
	m.Write(&b, visible)
	return b.String()
}

func TestHistogramAndCounters(t *testing.T) {
	m := New()
	m.ObserveRequest("GET", "/api/v1/machines", 200, 0.02)
	m.ObserveRequest("GET", "/api/v1/machines", 200, 0.3)
	m.ObserveRequest("POST", "/api/v1/machines", 409, 20)
	m.RateLimited()
	m.Operation("create")
	m.Inc("kryton_x_total", "x things", map[string]string{"k": "v"})
	out := render(m, nil)
	for _, want := range []string{
		`kryton_api_requests_total{method="GET",route="/api/v1/machines",code="200"} 2`,
		`kryton_api_requests_total{method="POST",route="/api/v1/machines",code="409"} 1`,
		`kryton_http_request_duration_seconds_bucket{method="GET",route="/api/v1/machines",le="0.025"} 1`,
		`kryton_http_request_duration_seconds_bucket{method="GET",route="/api/v1/machines",le="0.5"} 2`,
		`kryton_http_request_duration_seconds_bucket{method="POST",route="/api/v1/machines",le="+Inf"} 1`,
		`kryton_http_request_duration_seconds_count{method="GET",route="/api/v1/machines"} 2`,
		`kryton_rate_limit_rejections_total 1`,
		`kryton_operations_total{operation="create"} 1`,
		`kryton_x_total{k="v"} 1`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestCollectorVisibility(t *testing.T) {
	m := New()
	m.AddCollector(func(visible func(string) bool) []Family {
		f := Family{Name: "kryton_machines", Help: "machines"}
		for _, p := range []string{"a", "b"} {
			if visible == nil || visible(p) {
				f.Samples = append(f.Samples, Sample{Labels: map[string]string{"project": p}, Value: 2})
			}
		}
		return []Family{f}
	})
	all := render(m, nil)
	if !strings.Contains(all, `kryton_machines{project="a"} 2`) || !strings.Contains(all, `project="b"`) {
		t.Fatalf("expected both projects:\n%s", all)
	}
	only := render(m, func(p string) bool { return p == "a" })
	if strings.Contains(only, `project="b"`) || !strings.Contains(only, `project="a"`) {
		t.Fatalf("visibility filter failed:\n%s", only)
	}
}
