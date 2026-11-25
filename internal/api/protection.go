// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"net/http"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/model"
)

type protectionRequest struct {
	// Protected is required, so an empty body cannot remove a protection.
	Protected *bool  `json:"protected"`
	Reason    string `json:"reason,omitempty"`
}

type protectionResponse struct {
	Project    string                   `json:"project"`
	MachineID  string                   `json:"machineId"`
	Protected  bool                     `json:"protected"`
	Protection *model.MachineProtection `json:"protection,omitempty"`
}

// applyProtection overlays delete protection onto a machine response and
// drops "delete" from its allowed actions.
func (s *Server) applyProtection(m *model.Machine) {
	if s.protections == nil || m == nil {
		return
	}
	p, ok := s.protections.Get(m.Project, m.ID)
	if !ok {
		return
	}
	m.Protection = &model.MachineProtection{Reason: p.Reason, SetBy: p.SetBy, SetAt: p.SetAt}
	var allowed []string
	for _, a := range m.AllowedActions {
		if a != "delete" {
			allowed = append(allowed, a)
		}
	}
	m.AllowedActions = allowed
}

// setMachineProtection turns delete protection on or off.
func (s *Server) setMachineProtection(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Operator)
	if !ok {
		return
	}
	if s.protections == nil {
		s.writeAPIError(w, r, http.StatusNotImplemented, "NOT_CONFIGURED", "delete protection is not enabled")
		return
	}
	var req protectionRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	if req.Protected == nil {
		s.badRequest(w, r, `"protected" (true or false) is required`)
		return
	}
	if !*req.Protected && req.Reason != "" {
		s.badRequest(w, r, "reason only applies when protecting a machine")
		return
	}
	id := r.PathValue("id")
	m, err := s.p.Get(r.Context(), project, id)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	out := protectionResponse{Project: project, MachineID: id, Protected: *req.Protected}
	eventType := "io.kryton.machine.protection.removed"
	if *req.Protected {
		p, err := s.protections.Set(project, id, req.Reason, auth.FromContext(r.Context()).Name)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		out.Protection = &model.MachineProtection{Reason: p.Reason, SetBy: p.SetBy, SetAt: p.SetAt}
		eventType = "io.kryton.machine.protection.enabled"
		auditDetail(r.Context(), "protected")
	} else {
		if err := s.protections.Clear(project, id); err != nil {
			s.writeErr(w, r, err)
			return
		}
		auditDetail(r.Context(), "protection removed")
	}
	s.metrics.Operation("protection-update")
	s.events.Publish(r.Context(), eventType, "machines/"+id, map[string]any{"project": project, "machineId": id, "name": m.Spec.Name, "reason": req.Reason})
	jsonResponse(w, http.StatusOK, out)
}
