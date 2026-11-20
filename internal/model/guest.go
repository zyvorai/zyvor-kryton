// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-1.0

package model

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// MaxScriptBytes caps OEM scripts and guest job scripts.
const MaxScriptBytes = 64 * 1024

// Guest job states.
const (
	GuestJobQueued    = "queued"
	GuestJobRunning   = "running"
	GuestJobSucceeded = "succeeded"
	GuestJobFailed    = "failed"
	GuestJobTimedOut  = "timed_out"
	GuestJobCanceled  = "canceled"
)

// GuestJobTerminal reports whether state is final.
func GuestJobTerminal(state string) bool {
	switch state {
	case GuestJobSucceeded, GuestJobFailed, GuestJobTimedOut, GuestJobCanceled:
		return true
	}
	return false
}

// GuestJobSpec is a script submitted to run inside a guest.
type GuestJobSpec struct {
	Name           string            `json:"name,omitempty"`
	Shell          string            `json:"shell,omitempty"` // cmd (default) | powershell
	Script         string            `json:"script"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"` // default 600, max 86400
	Env            map[string]string `json:"env,omitempty"`
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
var jobName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_. -]{0,62}$`)

// Normalize applies defaults and validates the spec.
func (s *GuestJobSpec) Normalize() error {
	if s.Shell == "" {
		s.Shell = "cmd"
	}
	if s.TimeoutSeconds == 0 {
		s.TimeoutSeconds = 600
	}
	var problems []string
	if s.Shell != "cmd" && s.Shell != "powershell" {
		problems = append(problems, "shell must be cmd or powershell")
	}
	if strings.TrimSpace(s.Script) == "" || len(s.Script) > MaxScriptBytes || strings.ContainsRune(s.Script, 0) {
		problems = append(problems, "script must be non-empty text up to 65536 bytes")
	}
	if s.TimeoutSeconds < 1 || s.TimeoutSeconds > 86400 {
		problems = append(problems, "timeoutSeconds must be between 1 and 86400")
	}
	if s.Name != "" && !jobName.MatchString(s.Name) {
		problems = append(problems, "name must be up to 63 letters, digits, space, dot, dash or underscore")
	}
	if len(s.Env) > 32 {
		problems = append(problems, "env supports at most 32 variables")
	}
	for k, v := range s.Env {
		if !envName.MatchString(k) || strings.HasPrefix(strings.ToUpper(k), "KRYTON_") {
			problems = append(problems, fmt.Sprintf("invalid env name %q", k))
		}
		if len(v) > 4096 || strings.ContainsAny(v, "\x00\r\n") {
			problems = append(problems, fmt.Sprintf("invalid env value for %q", k))
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// GuestJob is a script execution inside a dockur guest.
type GuestJob struct {
	ID             string       `json:"id"`
	Project        string       `json:"project"`
	MachineID      string       `json:"machineId"`
	Name           string       `json:"name,omitempty"`
	Shell          string       `json:"shell"`
	State          string       `json:"state"`
	ExitCode       *int         `json:"exitCode,omitempty"`
	TimeoutSeconds int          `json:"timeoutSeconds"`
	Message        string       `json:"message,omitempty"`
	SubmittedAt    time.Time    `json:"submittedAt"`
	StartedAt      *time.Time   `json:"startedAt,omitempty"`
	FinishedAt     *time.Time   `json:"finishedAt,omitempty"`
	CancelRequest  bool         `json:"cancelRequested,omitempty"`
	Notified       bool         `json:"notified,omitempty"` // completion event already published
	Artifacts      []SharedFile `json:"artifacts,omitempty"`
}

// SharedFile describes an entry in a machine's shared folder.
type SharedFile struct {
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Size    int64     `json:"size"`
	IsDir   bool      `json:"isDir,omitempty"`
	ModTime time.Time `json:"modTime"`
}

// ReadinessCheck is one probe of Readiness.
type ReadinessCheck struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

// InstallProgress is a best-effort reading of dockur's container log.
type InstallProgress struct {
	Phase   string `json:"phase"` // starting|downloading|extracting|building|installing|stopped|unknown|complete
	Percent int    `json:"percent"`
	Message string `json:"message,omitempty"`
}

// Readiness is the result of GET /machines/{id}/ready.
type Readiness struct {
	MachineID     string           `json:"machineId"`
	Ready         bool             `json:"ready"`
	ContainerUp   bool             `json:"containerUp"`
	RDPOpen       bool             `json:"rdpOpen"`
	AgentPresent  bool             `json:"agentPresent"`
	AgentRequired bool             `json:"agentRequired"`
	Install       *InstallProgress `json:"install,omitempty"`
	Checks        []ReadinessCheck `json:"checks"`
}

// ValidateRelPath checks that p is a clean relative path: no absolute
// roots, drive letters, backslashes, "..", NUL/control characters. With
// allowNested false only a single path segment is accepted.
func ValidateRelPath(p string, allowNested bool) error {
	if p == "" {
		return errors.New("path is empty")
	}
	if len(p) > 1024 {
		return errors.New("path is too long")
	}
	if strings.HasPrefix(p, "/") {
		return errors.New("must be a relative path (host paths are not accepted)")
	}
	segs := strings.Split(p, "/")
	if !allowNested && len(segs) != 1 {
		return errors.New("must be a single directory name under the machine directory")
	}
	if len(segs) > 32 {
		return errors.New("path is too deep")
	}
	for _, s := range segs {
		if s == "" || s == "." || s == ".." {
			return errors.New("path must not contain empty, . or .. segments")
		}
		if len(s) > 255 || strings.ContainsAny(s, "\\:*?\"<>|") {
			return errors.New("path contains invalid characters")
		}
		for _, r := range s {
			if r < 0x20 || r == 0x7f {
				return errors.New("path contains control characters")
			}
		}
	}
	return nil
}
