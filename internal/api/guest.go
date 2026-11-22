// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/zyvorai/kryton/internal/auth"
	"github.com/zyvorai/kryton/internal/model"
	"github.com/zyvorai/kryton/internal/provider"
)

// DefaultMaxUploadBytes caps one shared-folder upload when Config.MaxUploadBytes is unset.
const DefaultMaxUploadBytes int64 = 1 << 30

type restartRequest struct {
	Force          bool  `json:"force,omitempty"`
	Wait           *bool `json:"wait,omitempty"`
	TimeoutSeconds int   `json:"timeoutSeconds,omitempty"`
}

type restartResponse struct {
	Machine   *model.Machine   `json:"machine"`
	Readiness *model.Readiness `json:"readiness,omitempty"`
}

func (s *Server) guestProvider(w http.ResponseWriter, r *http.Request) (provider.GuestProvider, bool) {
	gp, ok := s.p.(provider.GuestProvider)
	if !ok {
		s.writeAPIError(w, r, http.StatusNotImplemented, "UNSUPPORTED", "guest management is only available with the dockur provider", "Set KRYTON_PROVIDER=dockur.")
		return nil, false
	}
	return gp, true
}

func (s *Server) maxUploadBytes() int64 {
	if s.maxUpload > 0 {
		return s.maxUpload
	}
	return DefaultMaxUploadBytes
}

func timeoutParam(r *http.Request, def, max time.Duration) time.Duration {
	if v, err := strconv.Atoi(r.URL.Query().Get("timeoutSeconds")); err == nil && v > 0 {
		d := time.Duration(v) * time.Second
		if d > max {
			return max
		}
		return d
	}
	return def
}

// machineReady serves GET /machines/{id}/ready: 200 when the guest is
// ready, 503 with the same body otherwise (so it works as a probe).
// ?wait=true long-polls up to timeoutSeconds (default 120, max 900).
func (s *Server) machineReady(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	var rd *model.Readiness
	var err error
	if r.URL.Query().Get("wait") == "true" {
		rd, err = gp.WaitReady(r.Context(), project, r.PathValue("id"), timeoutParam(r, 2*time.Minute, 15*time.Minute))
	} else {
		rd, err = gp.Ready(r.Context(), project, r.PathValue("id"))
	}
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	status := http.StatusOK
	if !rd.Ready {
		status = http.StatusServiceUnavailable
	}
	jsonResponse(w, status, rd)
}

func (s *Server) restartMachine(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Operator)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	var req restartRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &req); err != nil {
			s.badRequest(w, r, err.Error())
			return
		}
	}
	wait := req.Wait == nil || *req.Wait
	timeout := 5 * time.Minute
	if req.TimeoutSeconds > 0 {
		timeout = time.Duration(req.TimeoutSeconds) * time.Second
		if timeout > 30*time.Minute {
			timeout = 30 * time.Minute
		}
	}
	m, rd, err := gp.Restart(r.Context(), project, r.PathValue("id"), provider.RestartOptions{Force: req.Force, Wait: wait, Timeout: timeout})
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.metrics.Operation("restart")
	s.events.Publish(r.Context(), "io.kryton.machine.restarted", "machines/"+m.ID, map[string]any{"project": project, "machineId": m.ID, "name": m.Spec.Name, "state": m.State, "force": req.Force})
	status := http.StatusAccepted
	if rd != nil && rd.Ready {
		status = http.StatusOK
		s.events.Publish(r.Context(), "io.kryton.machine.ready", "machines/"+m.ID, map[string]any{"project": project, "machineId": m.ID, "name": m.Spec.Name})
	}
	jsonResponse(w, status, restartResponse{Machine: m, Readiness: rd})
}

func (s *Server) machineLogs(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Operator) // logs may contain the guest's setup output
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	tail, _ := strconv.Atoi(r.URL.Query().Get("tail"))
	out, err := gp.Logs(r.Context(), project, r.PathValue("id"), tail)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = fmt.Fprint(w, out)
}

// ---- shared folder ----

func (s *Server) sharedList(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	items, err := gp.ListShared(r.Context(), project, r.PathValue("id"), r.URL.Query().Get("path"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusOK, listResponse[model.SharedFile]{Items: items})
}

func (s *Server) sharedDownload(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	f, info, err := gp.OpenShared(r.Context(), project, r.PathValue("id"), r.PathValue("path"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	serveDownload(w, r, f, info)
}

func serveDownload(w http.ResponseWriter, r *http.Request, f http.File, info model.SharedFile) {
	// Guest-controlled content: never let a browser sniff or render it.
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", info.Name))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeContent(w, r, info.Name, info.ModTime, f)
}

func (s *Server) sharedUpload(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Operator)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	limit := s.maxUploadBytes()
	if r.ContentLength > limit {
		s.writeAPIError(w, r, http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE", fmt.Sprintf("upload exceeds %d bytes", limit), "Upload a smaller file or raise KRYTON_DOCKUR_MAX_UPLOAD_MIB.")
		return
	}
	// The server-wide read timeout is sized for JSON bodies, not uploads.
	_ = http.NewResponseController(w).SetReadDeadline(time.Now().Add(30 * time.Minute))
	r.Body = http.MaxBytesReader(w, r.Body, limit+1)
	overwrite := r.URL.Query().Get("overwrite") == "true"
	sf, err := gp.PutShared(r.Context(), project, r.PathValue("id"), r.PathValue("path"), r.Body, limit, overwrite)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.metrics.Operation("shared-upload")
	s.events.Publish(r.Context(), "io.kryton.machine.shared.uploaded", "machines/"+r.PathValue("id")+"/shared", map[string]any{"project": project, "machineId": r.PathValue("id"), "path": sf.Path, "size": sf.Size})
	jsonResponse(w, http.StatusCreated, sf)
}

func (s *Server) sharedDelete(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Operator)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	if err := gp.DeleteShared(r.Context(), project, r.PathValue("id"), r.PathValue("path")); err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// ---- guest jobs ----

func (s *Server) jobSubmit(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Operator)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	var spec model.GuestJobSpec
	if err := decodeJSON(w, r, &spec); err != nil {
		s.badRequest(w, r, err.Error())
		return
	}
	job, err := gp.SubmitJob(r.Context(), project, r.PathValue("id"), spec)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	s.metrics.Operation("guest-job")
	jsonResponse(w, http.StatusAccepted, job)
}

func (s *Server) jobList(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	items, err := gp.ListJobs(r.Context(), project, r.PathValue("id"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusOK, listResponse[model.GuestJob]{Items: items})
}

func (s *Server) jobGet(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	job, err := gp.GetJob(r.Context(), project, r.PathValue("id"), r.PathValue("jid"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusOK, job)
}

func (s *Server) jobCancel(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Operator)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	job, err := gp.CancelJob(r.Context(), project, r.PathValue("id"), r.PathValue("jid"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusOK, job)
}

func (s *Server) jobLog(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	offset, _ := strconv.ParseInt(r.URL.Query().Get("offset"), 10, 64)
	limit, _ := strconv.ParseInt(r.URL.Query().Get("limit"), 10, 64)
	b, total, err := gp.JobLog(r.Context(), project, r.PathValue("id"), r.PathValue("jid"), offset, limit)
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Log-Size", strconv.FormatInt(total, 10))
	w.Header().Set("X-Log-Next-Offset", strconv.FormatInt(max(offset, 0)+int64(len(b)), 10))
	_, _ = w.Write(b)
}

func (s *Server) jobArtifacts(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	items, err := gp.ListArtifacts(r.Context(), project, r.PathValue("id"), r.PathValue("jid"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	jsonResponse(w, http.StatusOK, listResponse[model.SharedFile]{Items: items})
}

func (s *Server) jobArtifactDownload(w http.ResponseWriter, r *http.Request) {
	project, ok := s.requireProject(w, r, auth.Viewer)
	if !ok {
		return
	}
	gp, ok := s.guestProvider(w, r)
	if !ok {
		return
	}
	f, info, err := gp.OpenArtifact(r.Context(), project, r.PathValue("id"), r.PathValue("jid"), r.PathValue("path"))
	if err != nil {
		s.writeErr(w, r, err)
		return
	}
	defer func() { _ = f.Close() }()
	serveDownload(w, r, f, info)
}
