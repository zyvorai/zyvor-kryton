// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package provider

import (
	"context"
	"io"
	"os"
	"time"

	"github.com/zyvorai/kryton/internal/model"
)

// RestartOptions controls GuestProvider.Restart.
type RestartOptions struct {
	// Force kills the container instead of asking Windows to shut down
	// via ACPI first.
	Force bool
	// Wait blocks until the guest is ready (RDP answering) or Timeout.
	Wait    bool
	Timeout time.Duration
}

// GuestProvider is implemented by providers (dockur) that can manage a
// guest beyond power state: readiness, a shared folder, and script jobs.
// Methods return provider.ErrNotFound for unknown machines, ErrInvalid for
// bad paths/specs, ErrTooLarge for oversize uploads and ErrPrecondition
// when the machine was created without the needed option.
type GuestProvider interface {
	Restart(ctx context.Context, project, id string, opts RestartOptions) (*model.Machine, *model.Readiness, error)
	Ready(ctx context.Context, project, id string) (*model.Readiness, error)
	WaitReady(ctx context.Context, project, id string, timeout time.Duration) (*model.Readiness, error)
	Logs(ctx context.Context, project, id string, tail int) (string, error)

	ListShared(ctx context.Context, project, id, dir string) ([]model.SharedFile, error)
	PutShared(ctx context.Context, project, id, rel string, r io.Reader, maxBytes int64, overwrite bool) (*model.SharedFile, error)
	OpenShared(ctx context.Context, project, id, rel string) (*os.File, model.SharedFile, error)
	DeleteShared(ctx context.Context, project, id, rel string) error

	SubmitJob(ctx context.Context, project, id string, spec model.GuestJobSpec) (*model.GuestJob, error)
	ListJobs(ctx context.Context, project, id string) ([]model.GuestJob, error)
	GetJob(ctx context.Context, project, id, jobID string) (*model.GuestJob, error)
	CancelJob(ctx context.Context, project, id, jobID string) (*model.GuestJob, error)
	// JobLog returns up to limit bytes of the job's stdout/stderr from
	// offset, plus the total log size.
	JobLog(ctx context.Context, project, id, jobID string, offset, limit int64) ([]byte, int64, error)
	ListArtifacts(ctx context.Context, project, id, jobID string) ([]model.SharedFile, error)
	OpenArtifact(ctx context.Context, project, id, jobID, rel string) (*os.File, model.SharedFile, error)
}

// EventSink receives CloudEvents-style notifications from a provider's
// background monitor (e.g. guest job completion).
type EventSink func(ctx context.Context, eventType, subject string, data map[string]any)
