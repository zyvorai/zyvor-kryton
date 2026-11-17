// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/webhooks"
)

type webhookCreateRequest struct {
	Project     string   `json:"project"`
	URL         string   `json:"url"`
	Description string   `json:"description"`
	EventTypes  []string `json:"eventTypes"`
}

type webhookUpdateRequest struct {
	URL         *string   `json:"url"`
	Description *string   `json:"description"`
	EventTypes  *[]string `json:"eventTypes"`
	Enabled     *bool     `json:"enabled"`
}

func (s *Server) webhooksReady(w http.ResponseWriter, r *http.Request) bool {
	if s.webhooks == nil {
		s.writeAPIError(w, r, http.StatusNotImplemented, "NOT_CONFIGURED", "webhooks are not enabled")
		return false
	}
	return true
}

// webhookErr maps manager errors onto API errors.
func (s *Server) webhookErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, webhooks.ErrNotFound):
		s.writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "webhook or delivery not found")
	case errors.Is(err, webhooks.ErrBlocked):
		s.writeAPIError(w, r, http.StatusBadRequest, "WEBHOOK_URL_BLOCKED", err.Error(),
			"Webhook URLs must be https and point to a public address. To permit an internal receiver, add its host or CIDR to KRYTON_WEBHOOK_ALLOW_HOSTS.")
	case errors.Is(err, webhooks.ErrLimit):
		s.writeAPIError(w, r, http.StatusConflict, "WEBHOOK_LIMIT", err.Error(), "Delete an unused subscription first.")
	default:
		s.badRequest(w, r, err.Error())
	}
}

// webhookSub loads a subscription and requires role on its project. A
// subscription the caller cannot access is reported as not found.
func (s *Server) webhookSub(w http.ResponseWriter, r *http.Request, role auth.Role) (webhooks.Subscription, bool) {
	sub, err := s.webhooks.Get(r.PathValue("id"))
	if err != nil || !auth.Can(auth.FromContext(r.Context()), sub.Project, role) {
		s.writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "webhook not found")
		return webhooks.Subscription{}, false
	}
	auditNote(r.Context(), sub.Project, "")
	return sub, true
}

func (s *Server) createWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksReady(w, r) {
		return
	}
	var req webhookCreateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	if req.Project == "" {
		req.Project = s.defaultProject
	}
	if !s.projectConfigured(req.Project) {
		s.badRequest(w, r, "project is not configured")
		return
	}
	auditNote(r.Context(), req.Project, "")
	p := auth.FromContext(r.Context())
	if !auth.Can(p, req.Project, auth.Admin) {
		s.forbidden(w, r)
		return
	}
	sub, err := s.webhooks.Create(webhooks.Subscription{Project: req.Project, URL: req.URL, Description: req.Description, EventTypes: req.EventTypes, CreatedBy: p.Name})
	if err != nil {
		s.webhookErr(w, r, err)
		return
	}
	// The secret is returned exactly once, here.
	jsonResponse(w, http.StatusCreated, sub)
}

func (s *Server) listWebhooks(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksReady(w, r) {
		return
	}
	project, ok := s.requireProject(w, r, auth.Operator)
	if !ok {
		return
	}
	jsonResponse(w, http.StatusOK, listResponse[webhooks.Subscription]{Items: s.webhooks.List(project, nil)})
}

func (s *Server) getWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksReady(w, r) {
		return
	}
	if sub, ok := s.webhookSub(w, r, auth.Operator); ok {
		jsonResponse(w, http.StatusOK, sub)
	}
}

func (s *Server) updateWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksReady(w, r) {
		return
	}
	sub, ok := s.webhookSub(w, r, auth.Admin)
	if !ok {
		return
	}
	var req webhookUpdateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	up, err := s.webhooks.Update(sub.ID, webhooks.Patch{URL: req.URL, Description: req.Description, EventTypes: req.EventTypes, Enabled: req.Enabled})
	if err != nil {
		s.webhookErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusOK, up)
}

func (s *Server) deleteWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksReady(w, r) {
		return
	}
	sub, ok := s.webhookSub(w, r, auth.Admin)
	if !ok {
		return
	}
	if err := s.webhooks.Delete(sub.ID); err != nil {
		s.webhookErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) testWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksReady(w, r) {
		return
	}
	sub, ok := s.webhookSub(w, r, auth.Admin)
	if !ok {
		return
	}
	d, err := s.webhooks.SendTest(sub.ID)
	if err != nil {
		s.webhookErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusAccepted, d.WithoutPayload())
}

func (s *Server) listWebhookDeliveries(w http.ResponseWriter, r *http.Request) {
	s.deliveries(w, r, r.URL.Query().Get("status"))
}

func (s *Server) listWebhookDeadLetters(w http.ResponseWriter, r *http.Request) {
	s.deliveries(w, r, webhooks.StatusDead)
}

func (s *Server) deliveries(w http.ResponseWriter, r *http.Request, status string) {
	if !s.webhooksReady(w, r) {
		return
	}
	project, ok := s.requireProject(w, r, auth.Operator)
	if !ok {
		return
	}
	switch status {
	case "", webhooks.StatusPending, webhooks.StatusRetrying, webhooks.StatusSucceeded, webhooks.StatusDead:
	default:
		s.badRequest(w, r, "status must be pending, retrying, succeeded or dead")
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	items, next, err := s.webhooks.ListDeliveries(webhooks.DeliveryFilter{Project: project, SubscriptionID: q.Get("subscriptionId"), Status: status, Limit: limit, Cursor: q.Get("cursor")})
	if err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, listResponse[webhooks.Delivery]{Items: items, NextCursor: next})
}

func (s *Server) deliveryFor(w http.ResponseWriter, r *http.Request, role auth.Role) (webhooks.Delivery, bool) {
	d, err := s.webhooks.GetDelivery(r.PathValue("id"))
	if err != nil || !auth.Can(auth.FromContext(r.Context()), d.Project, role) {
		s.writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "delivery not found")
		return webhooks.Delivery{}, false
	}
	auditNote(r.Context(), d.Project, "")
	return d, true
}

func (s *Server) getWebhookDelivery(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksReady(w, r) {
		return
	}
	if d, ok := s.deliveryFor(w, r, auth.Operator); ok {
		jsonResponse(w, http.StatusOK, d)
	}
}

func (s *Server) redeliverWebhook(w http.ResponseWriter, r *http.Request) {
	if !s.webhooksReady(w, r) {
		return
	}
	d, ok := s.deliveryFor(w, r, auth.Admin)
	if !ok {
		return
	}
	nd, err := s.webhooks.Redeliver(d.ID)
	if err != nil {
		s.webhookErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusAccepted, nd.WithoutPayload())
}
