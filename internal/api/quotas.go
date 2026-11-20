// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package api

import (
	"fmt"
	"net/http"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/quota"
)

type quotaView struct {
	Project  string       `json:"project"`
	Limits   quota.Limits `json:"limits"`
	Usage    quota.Usage  `json:"usage"`
	Explicit bool         `json:"explicit"` // false = inherited from the default
}

// enforceQuota checks the project's limits against current usage plus the
// requested machine. On success it returns an unlock func the caller must
// invoke once provider.Create has returned: creates in a project are
// serialized while a quota applies so two concurrent requests cannot both
// slip under the limit. On violation it has already written the response:
// 403 when the request alone exceeds a limit (it can never fit), 409 when it
// conflicts with current usage (it would fit after deleting something).
func (s *Server) enforceQuota(w http.ResponseWriter, r *http.Request, project string, spec model.MachineSpec) (func(), bool) {
	noop := func() {}
	if s.quotas == nil {
		return noop, true
	}
	limits := s.quotas.Get(project)
	if limits.IsZero() {
		return noop, true
	}
	s.createMu.Lock()
	ms, err := s.p.List(r.Context(), project)
	if err != nil {
		s.createMu.Unlock()
		s.writeErr(w, r, err)
		return nil, false
	}
	if v := quota.Check(limits, quota.UsageOf(ms), quota.RequestOf(spec)); v != nil {
		s.createMu.Unlock()
		status := http.StatusConflict
		hint := fmt.Sprintf("Delete or resize other machines in project %q, or ask an admin to raise the quota (PUT /api/v1/quotas/%s).", project, project)
		if v.Hard {
			status = http.StatusForbidden
			hint = fmt.Sprintf("This single machine is larger than the project's %s limit of %d; request a smaller machine or ask an admin to raise the quota.", v.Resource, v.Limit)
		}
		s.metrics.Inc("kryton_quota_rejections_total", "Machine creates rejected by a project quota.", map[string]string{"project": project, "resource": v.Resource})
		auditDetail(r.Context(), v.Error())
		s.writeAPIError(w, r, status, "QUOTA_EXCEEDED", v.Error(), hint)
		return nil, false
	}
	return s.createMu.Unlock, true
}

func (s *Server) quotaFor(r *http.Request, project string) (quotaView, error) {
	ms, err := s.p.List(r.Context(), project)
	if err != nil {
		return quotaView{}, err
	}
	return quotaView{Project: project, Limits: s.quotas.Get(project), Usage: quota.UsageOf(ms), Explicit: s.quotas.Explicit(project)}, nil
}

func (s *Server) listQuotas(w http.ResponseWriter, r *http.Request) {
	if s.quotas == nil {
		s.writeAPIError(w, r, http.StatusNotImplemented, "NOT_CONFIGURED", "quotas are not enabled")
		return
	}
	p := auth.FromContext(r.Context())
	items := []quotaView{}
	for _, project := range s.projects {
		if !auth.Can(p, project, auth.Viewer) {
			continue
		}
		v, err := s.quotaFor(r, project)
		if err != nil {
			s.writeErr(w, r, err)
			return
		}
		items = append(items, v)
	}
	jsonResponse(w, http.StatusOK, listResponse[quotaView]{Items: items})
}

func (s *Server) quotaProject(w http.ResponseWriter, r *http.Request, role auth.Role) (string, bool) {
	if s.quotas == nil {
		s.writeAPIError(w, r, http.StatusNotImplemented, "NOT_CONFIGURED", "quotas are not enabled")
		return "", false
	}
	project := r.PathValue("project")
	if !s.projectConfigured(project) {
		s.badRequest(w, r, "project is not configured")
		return "", false
	}
	auditNote(r.Context(), project, "")
	if !auth.Can(auth.FromContext(r.Context()), project, role) {
		s.forbidden(w, r)
		return "", false
	}
	return project, true
}

func (s *Server) getQuota(w http.ResponseWriter, r *http.Request) {
	project, ok := s.quotaProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	v, err := s.quotaFor(r, project)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusOK, v)
}

func (s *Server) putQuota(w http.ResponseWriter, r *http.Request) {
	project, ok := s.quotaProject(w, r, auth.Admin)
	if !ok {
		return
	}
	var l quota.Limits
	if err := decodeJSON(w, r, &l); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	if err := l.Validate(); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	if err := s.quotas.Set(project, l); err != nil {
		s.writeErr(w, r, err)
		return
	}
	v, err := s.quotaFor(r, project)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.events.Publish(r.Context(), "io.kryton.quota.updated", "quotas/"+project, map[string]any{"project": project, "limits": l})
	jsonResponse(w, http.StatusOK, v)
}

func (s *Server) deleteQuota(w http.ResponseWriter, r *http.Request) {
	project, ok := s.quotaProject(w, r, auth.Admin)
	if !ok {
		return
	}
	if err := s.quotas.Delete(project); err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.events.Publish(r.Context(), "io.kryton.quota.updated", "quotas/"+project, map[string]any{"project": project, "removed": true})
	w.WriteHeader(http.StatusNoContent)
}
