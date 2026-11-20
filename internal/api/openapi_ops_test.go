// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package api

import (
	"strings"
	"testing"
)

// TestOpenAPICoversOperationsEndpoints keeps openapi.yaml in sync with the
// routes the server registers for operations and observability.
func TestOpenAPICoversOperationsEndpoints(t *testing.T) {
	spec := string(openAPISpec)
	for _, ep := range allAPIEndpoints() {
		switch ep.Group {
		case "operations", "webhooks", "schedules", "quotas":
		default:
			if !strings.HasSuffix(ep.Path, "/ttl") {
				continue
			}
		}
		idx := strings.Index(spec, "\n  "+ep.Path+":\n")
		if idx < 0 {
			t.Errorf("openapi.yaml is missing path %s", ep.Path)
			continue
		}
		// Slice out this path item (up to the next top-level path) and look for the verb.
		rest := spec[idx+1:]
		if end := strings.Index(rest[1:], "\n  /"); end >= 0 {
			rest = rest[:end+1]
		}
		if !strings.Contains(rest, "\n    "+strings.ToLower(ep.Method)+":") {
			t.Errorf("openapi.yaml path %s has no %s operation", ep.Path, ep.Method)
		}
	}
	for _, schema := range []string{"AuditEntry", "WebhookSubscription", "WebhookDelivery", "Schedule", "QuotaLimits", "ProjectQuota"} {
		if !strings.Contains(spec, "\n    "+schema+":\n") {
			t.Errorf("openapi.yaml is missing schema %s", schema)
		}
	}
}
