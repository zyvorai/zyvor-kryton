// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"errors"
	"net/http"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/schedule"
)

type scheduleCreateRequest struct {
	Project      string `json:"project"`
	MachineID    string `json:"machineId"`
	Action       string `json:"action"`
	Cron         string `json:"cron"`
	Timezone     string `json:"timezone"`
	SnapshotName string `json:"snapshotName"`
}

type scheduleUpdateRequest struct {
	Action       *string `json:"action"`
	Cron         *string `json:"cron"`
	Timezone     *string `json:"timezone"`
	SnapshotName *string `json:"snapshotName"`
	Enabled      *bool   `json:"enabled"`
}

func (s *Server) schedulesReady(w http.ResponseWriter, r *http.Request) bool {
	if s.schedules == nil {
		s.writeAPIError(w, r, http.StatusNotImplemented, "NOT_CONFIGURED", "scheduled actions are not enabled")
		return false
	}
	return true
}

func (s *Server) scheduleErr(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, schedule.ErrNotFound):
		s.writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "schedule not found")
	case errors.Is(err, schedule.ErrLimit):
		s.writeAPIError(w, r, http.StatusConflict, "SCHEDULE_LIMIT", err.Error(), "Delete an unused schedule first.")
	default:
		s.badRequest(w, r, err.Error())
	}
}

func (s *Server) scheduleFor(w http.ResponseWriter, r *http.Request, role auth.Role) (schedule.Schedule, bool) {
	sc, err := s.schedules.Get(r.PathValue("id"))
	if err != nil || !auth.Can(auth.FromContext(r.Context()), sc.Project, role) {
		s.writeAPIError(w, r, http.StatusNotFound, "NOT_FOUND", "schedule not found")
		return schedule.Schedule{}, false
	}
	auditNote(r.Context(), sc.Project, sc.MachineID)
	return sc, true
}

func (s *Server) createSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.schedulesReady(w, r) {
		return
	}
	var req scheduleCreateRequest
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
	auditNote(r.Context(), req.Project, req.MachineID)
	p := auth.FromContext(r.Context())
	if !auth.Can(p, req.Project, auth.Operator) {
		s.forbidden(w, r)
		return
	}
	if req.MachineID != "" {
		if _, err := s.p.Get(r.Context(), req.Project, req.MachineID); err != nil {
			s.writeErr(w, r, err)
			return
		}
	}
	sc, err := s.schedules.Create(schedule.Schedule{Project: req.Project, MachineID: req.MachineID, Action: req.Action,
		Cron: req.Cron, Timezone: req.Timezone, SnapshotName: req.SnapshotName, CreatedBy: p.Name})
	if err != nil {
		s.scheduleErr(w, r, err)
		return
	}
	s.events.Publish(r.Context(), "io.kryton.schedule.created", "schedules/"+sc.ID, map[string]any{"project": sc.Project, "scheduleId": sc.ID, "machineId": sc.MachineID, "action": sc.Action, "cron": sc.Cron, "timezone": sc.Timezone})
	jsonResponse(w, http.StatusCreated, sc)
}

func (s *Server) listSchedules(w http.ResponseWriter, r *http.Request) {
	if !s.schedulesReady(w, r) {
		return
	}
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	jsonResponse(w, http.StatusOK, listResponse[schedule.Schedule]{Items: s.schedules.List(project, r.URL.Query().Get("machineId"), nil)})
}

func (s *Server) getSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.schedulesReady(w, r) {
		return
	}
	if sc, ok := s.scheduleFor(w, r, auth.Viewer); ok {
		jsonResponse(w, http.StatusOK, sc)
	}
}

func (s *Server) updateSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.schedulesReady(w, r) {
		return
	}
	sc, ok := s.scheduleFor(w, r, auth.Operator)
	if !ok {
		return
	}
	var req scheduleUpdateRequest
	if err := decodeJSON(w, r, &req); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	up, err := s.schedules.Update(sc.ID, schedule.Patch{Action: req.Action, Cron: req.Cron, Timezone: req.Timezone, SnapshotName: req.SnapshotName, Enabled: req.Enabled})
	if err != nil {
		s.scheduleErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusOK, up)
}

func (s *Server) deleteSchedule(w http.ResponseWriter, r *http.Request) {
	if !s.schedulesReady(w, r) {
		return
	}
	sc, ok := s.scheduleFor(w, r, auth.Operator)
	if !ok {
		return
	}
	if err := s.schedules.Delete(sc.ID); err != nil {
		s.scheduleErr(w, r, err)
		return
	}
	s.events.Publish(r.Context(), "io.kryton.schedule.deleted", "schedules/"+sc.ID, map[string]any{"project": sc.Project, "scheduleId": sc.ID, "machineId": sc.MachineID})
	w.WriteHeader(http.StatusNoContent)
}
