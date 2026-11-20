// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package api

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestOpenAPIDocumentsGuestEndpoints(t *testing.T) {
	h, _ := testServer(t)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/openapi.yaml", nil))
	spec := w.Body.String()
	for _, p := range []string{
		"/api/v1/machines/{id}/restart:", "/api/v1/machines/{id}/ready:", "/api/v1/machines/{id}/logs:",
		"/api/v1/machines/{id}/shared:", "/api/v1/machines/{id}/shared/{path}:",
		"/api/v1/machines/{id}/jobs:", "/api/v1/machines/{id}/jobs/{jid}:", "/api/v1/machines/{id}/jobs/{jid}/cancel:",
		"/api/v1/machines/{id}/jobs/{jid}/logs:", "/api/v1/machines/{id}/jobs/{jid}/artifacts:",
		"/api/v1/machines/{id}/jobs/{jid}/artifacts/{path}:", "GuestJobSpec:", "Readiness:", "publicBind:", "jobAgent:",
	} {
		if !strings.Contains(spec, p) {
			t.Errorf("openapi.yaml missing %s", p)
		}
	}
}
