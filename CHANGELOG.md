# Changelog

All notable changes to Kryton are documented here, in [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format. Kryton follows [Semantic Versioning](https://semver.org/).

For narrative release write-ups (what changed and why, aimed at operators), see [RELEASE_NOTES.md](RELEASE_NOTES.md). This file is the flat, version-by-version index; RELEASE_NOTES.md is the prose companion — keep both in sync when cutting a release.

## [Unreleased]

### Added

- Label selectors: `GET /api/v1/machines?selector=env=ci,tier!=db` (Kubernetes-style `=`, `==`, `!=`, `in`, `notin`, key, `!key`; capped at 1024 bytes, 16 requirements, 16 values). `krytonctl list --selector`, `krytonctl create --label k=v`, and the web console search now matches `key=value` labels. `labels` is now documented in the OpenAPI create schema.
- `Idempotency-Key` on `POST /api/v1/machines`: a retried create returns the original `201` with `Idempotent-Replay: true` instead of a duplicate or a `409`; reuse with a different body is `422 IDEMPOTENCY_KEY_REUSED`, an in-flight retry `409 IDEMPOTENCY_IN_PROGRESS`. Keys are per caller and project, kept 24 h in memory (lost on restart), at most 10,000; a replay never repeats the one-time generated password. `krytonctl create --idempotency-key`.
- Bulk lifecycle: `POST /api/v1/machines/actions` starts or stops up to 100 machines by `ids` or `selector` with per-machine results; `krytonctl bulk`.
- Event filters on `GET /api/v1/events`: `type` (exact or trailing `*`), `machineId`, `since`; `krytonctl events --type --machine --since`.
- Bulk actions grow: `POST /api/v1/machines/actions` now also runs `snapshot` (with `snapshotName`, returning each `snapshotId`) and `delete`. `dryRun: true` validates every target against its state and changes nothing (EC2 `DryRun` style); a real bulk delete requires `confirm: true`. `krytonctl bulk delete|snapshot --dry-run --confirm --snapshot-name`.
- `allowedActions` on every machine in `GET /machines` and `GET /machines/{id}` (Redfish-style discoverability), derived from state and provider support.
- List filters `state` and `name` on `GET /machines`; `krytonctl list --state --name`.
- `GET /api/v1/summary?groupBy=<label key>` breaks the totals down per label value; `krytonctl summary --group-by`.
- `GET /api/v1/events/stream` accepts the `type` and `machineId` filters of `GET /events`.
- `docs/RESEARCH.md`: survey of machine-management APIs with ranked ideas and sources.

### Fixed

- `spec.labels` had no size cap: a create request could carry thousands of labels. At most 64 are accepted now (`400 INVALID_REQUEST`).
- OpenAPI: the create schema omitted `labels`.
- Golden-build and storage-setup status files were rewritten in place, so a poller could read a truncated file and fail with `unexpected end of JSON input` (flaky `internal/golden` and `internal/storage` tests, and a transient error in the status endpoints). They are now written to a temp file and renamed.

### Changed

- Operator console deps (`web/`): vitest 5.0.3 -> 5.1.1 and @vitejs/plugin-react 6.1.1 -> 6.3.0; embedded bundle in `cmd/krytond/web` rebuilt (typecheck, 38 tests, build pass).
- Docs CI: Python pin `3.x` -> `3.14`; `requirements-docs.txt` floors raised to mkdocs>=1.6.1 and mkdocs-material>=9.8.2 (current PyPI releases).

### Dependency audit, held back or already current

- Go modules: `go get -u ./... && go mod tidy` changed nothing; gorilla/websocket v1.5.3 and golang.org/x/time v0.16.0 are latest and neither has a newer major module path.
- Go toolchain: go.mod `go 1.27.1` stays (documented minimum; 1.27.4 is the latest release and the Docker/govulncheck pin). No 1.26.x CI matrix: the module requires 1.27, so a 1.26 leg could not build.
- Dockerfile: golang:1.27.4 digest re-verified against Docker Hub (unchanged); distroless digest unchanged. Helm has no third-party images.
- GitHub Actions: all pins already at the verified majors. Unverified against sibling repos and left as is: golangci/golangci-lint-action v9 (latest v9.3.0), golang/govulncheck-action v1 (v1.1.0), aquasecurity/trivy-action v0.36.0 (latest), azure/setup-kubectl v5 (v5.2.0).
- Node 24 pin already present in CI; local verification ran on Node 22.
- vendored noVNC stays at 1.5.0 (bundled build needs a reviewed rebuild).

## [1.4.4] - 2026-11-18

### Fixed

- Deleting a dockur machine could leave its container running: `compose down` ran on the request context and its error was ignored, so a client that disconnected during the graceful shutdown deleted the record and data but not the guest. The teardown now survives a client disconnect, and a failed teardown keeps the machine so the delete can be retried.

## [1.4.3] - 2026-11-18

### Fixed

- dockur guest jobs stayed `queued` until the guest rebooted. Jobs are created on the host outside Samba, so the guest's SMB client served a stale listing of `.kryton/jobs` (directory leases). The agent now creates and deletes a scratch file in that directory before each poll, which refreshes the listing; verified on a real Windows 11 guest (fresh jobs start in 2 to 10 s). Machines created earlier keep the old agent; see `docs/DOCKUR.md`.
- `POST /machines/{id}/restart` could leave the guest powered off if the client disconnected between the stop and the start; both now run on a context detached from the request (the readiness wait still honours it).

### Docs

- `docs/DOCKUR.md`: job scripts should use `\\host.lan\Data`; `Z:` is not mapped in the agent session.

## [1.4.2] - 2026-11-18

### Fixed

- CI: staticcheck S1021 in the audit test and gosec G703/G304 findings on operator-chosen file paths in `krytonctl` and config loading (annotated with `#nosec` and a reason). No runtime behaviour change; 1.4.1 shipped with a red CI run, 1.4.2 is the first green tag of this line.

## [1.4.1] - 2026-11-17

### Changed

- Web console: the dockur create form drops the legacy host-folder fields (`sharedDir` / `oemDir`, which now return 400 for host paths) and offers `shared`, `oemScript` (paste or load an `install.bat`), `jobAgent`, `publicBind` (off by default, with a warning) and optional `rdpPort` / `consolePort`. The password is optional; when left empty the generated password is shown once in a copy-once dialog. The old `admin` default in the form is gone.
- Web console, machine detail (dockur): readiness and install progress from `GET /ready` (polled while installing), a Restart button (graceful, with a force option), a Jobs panel (list, submit with `cmd` or `powershell` and a timeout, logs, cancel) and a Shared folder panel (browse, upload, download, delete). Mutating controls are hidden for viewer keys. Embedded bundle rebuilt.

### Docs

- `docs/DOCKUR.md`: new "Operating notes from a real Windows lab" (what `ready` means, first-boot script timing, storage speed, blank viewer, deleting machines, Compose requirement, lab auto-auth on public hosts).

## [1.4.0] - 2026-11-17

### Changed

- Build image: `golang` 1.27.3 -> 1.27.4 (digest re-resolved from Docker Hub). Fixes GO-2026-6859 (net/http MaxBytesReader header race), which govulncheck flagged as reachable on Go 1.27.2.
- Runtime image: `gcr.io/distroless/static-debian13:nonroot` digest re-resolved to the current tag digest (`aafe9936...`); still pinned by `@sha256`.
- CI: govulncheck now scans with Go 1.27.4 (was 1.27.1); golangci-lint v2.14.0 -> v2.15.0 (linter bumps only; 0 new findings); gosec v2.29.0 -> v2.30.0 (rule fixes and G125; 0 findings).
- Operator console deps (`web/`): react and react-dom 19.3.0 -> 19.3.1 (RSC transport only, no client behaviour change); vite 8.3.2 -> 8.3.8 (patch fixes). Embedded bundle in `cmd/krytond/web` rebuilt.
- Unchanged because already current: gorilla/websocket v1.5.3, golang.org/x/time v0.16.0, every GitHub Action major, mkdocs 1.6.1 / mkdocs-material 9.8.1 (ranges unchanged; docs build verified). The `go` directive stays at 1.27.1 (documented minimum).
- Not bumped: vitest 5.0.3 -> 5.1.0 and @vitejs/plugin-react 6.1.1 -> 6.3.0 (minor releases, left for dependabot/a separate change); vendored noVNC stays at 1.5.0 (1.7.0 exists, but `web/public/novnc-rfb.js` is a bundled build and needs a reviewed rebuild; MPL-2.0 license file unchanged).
- **Security:** dockur RDP and web-viewer ports bind to `127.0.0.1` by default; `dockur.publicBind` opts into `0.0.0.0`. `rdpHost` reflects the bind.
- **Security:** when `dockur.password` is omitted a random 20-character password is generated and returned once as `generatedPassword` (the `admin` default is gone).
- **Breaking:** `dockur.sharedDir` / `dockur.oemDir` no longer accept host paths (they were an arbitrary host mount); they name a single directory under the machine directory. Use `shared` / `oemScript`.
- Explicit or auto-allocated RDP/console ports are checked against all machines and the host; a clash is `409 CONFLICT` with the reason. Provider errors `ErrInvalid` (400), `ErrTooLarge` (413) and `ErrPrecondition` (409) are mapped by the API.
- dockur `start` leaves the machine `starting` until RDP answers; machine state flips to `running` on an RDP handshake rather than on the web viewer responding.
- **`/metrics` is no longer anonymous when API auth is enabled.** Set `KRYTON_METRICS_TOKEN` (and `metrics.tokenSecret` in Helm) for Prometheus, or `KRYTON_METRICS_PUBLIC=true` to keep the old behaviour. With `KRYTON_AUTH_MODE=disabled` it stays open.
- `role` is optional in `keys.json`: a key that lists `projects` without a `role` is treated as `admin`, matching how every existing key behaved. Existing files need no change.

### Added

- dockur provider: first-class shared folder (`dockur.shared`, `/shared` = `\\host.lan\Data`) with list/download/upload/delete endpoints (`/machines/{id}/shared`), and an OEM first-boot script (`dockur.oemScript` -> `/oem/install.bat`). Host paths stay under the machine directory: `..`, absolute paths, backslashes, drive letters and symlinks are rejected; uploads are size-limited (`KRYTON_DOCKUR_MAX_UPLOAD_MIB`, default 1024), atomic, role- and project-scoped.
- dockur guest jobs API (`/machines/{id}/jobs`, logs, artifacts, cancel) driven by a PowerShell guest agent installed through the OEM script (`dockur.jobAgent`); timeouts enforced by agent and host; `io.kryton.machine.job.submitted` / `.completed` CloudEvents from a background monitor.
- `POST /machines/{id}/restart` (graceful ACPI shutdown then start, `force`, wait for RDP), `GET /machines/{id}/ready` probe (container, RDP X.224 handshake, agent heartbeat, install progress parsed from dockur logs), `GET /machines/{id}/logs`; events `io.kryton.machine.restarted` / `.ready`.
- `krytonctl`: `ready`, `restart`, `logs`, `shared-ls/put/get/rm`, `job-run` (`--wait` mirrors the job's exit code), `jobs`, `job-get/logs/cancel/artifacts/artifact`; create flags `--dockur-shared`, `--dockur-oem-script`, `--dockur-job-agent`, `--dockur-public-bind`, `--dockur-rdp-port`, `--dockur-console-port`.
- Catalog: Windows 11 images labelled 25H2, Windows 11/10 LTSC editions, plus Tiny10 and Windows Server 2012; dockur VERSION codes verified against the dockur/windows README. ARM64 images are rejected as unsupported.
- Fake container-runtime test seam (`dockur.Config.Hooks`) and tests for every new path; OpenAPI entries for all new endpoints.
- Prometheus `/metrics` expanded: machines by project/provider/state, provider health (`kryton_provider_up`), per-route request counts and latency histograms, rate-limit rejections, auth failures, quota usage/limits, webhook and scheduler counters. Protected by a dedicated `KRYTON_METRICS_TOKEN` (or any API key, scoped to its projects). The Helm ServiceMonitor now presents the token (`metrics.tokenSecret`).
- Append-only audit log of every mutating API call (and scheduler/TTL actions): `GET /api/v1/audit` with filters (project, actor, action, machine, result, time range) and cursor pagination; optional size-rotated JSONL sink (`KRYTON_AUDIT_FILE`, `KRYTON_AUDIT_MAX_FILE_BYTES`, `KRYTON_AUDIT_MAX_FILES`). `krytonctl audit`.
- Per-project outbound webhooks (`/api/v1/webhooks…`) for CloudEvents: glob event filters, HMAC-SHA256 signed deliveries (timestamp + body), exponential-backoff retries, delivery log, dead-letter list and redelivery, test deliveries, and an SSRF guard (https only, no private/loopback/link-local/metadata ranges, re-checked at dial time; `KRYTON_WEBHOOK_ALLOW_HOSTS` to allow internal receivers). `krytonctl webhooks`.
- Scheduled power actions (`/api/v1/schedules…`): cron expressions in any IANA timezone running start, stop, restart or snapshot, with DST-correct wall-clock semantics and a catch-up window that skips stale runs after downtime. `krytonctl schedules`.
- Machine TTL hygiene: `PUT /api/v1/machines/{id}/ttl` to extend or clear a TTL, `KRYTON_DEFAULT_TTL_MINUTES`, and an `io.kryton.machine.expiring` warning event (`KRYTON_TTL_WARN`). Automatic expiries are audited. `krytonctl ttl`.
- Per-project quotas on machines, vCPU, memory and disk (`/api/v1/quotas…`, `KRYTON_QUOTA_MAX_*`, `KRYTON_QUOTAS_FILE`), enforced at create with `QUOTA_EXCEEDED`: 409 when current usage is the obstacle, 403 when a single machine can never fit. `krytonctl quota`.
- Per-project API-key roles: `grants` in `keys.json` (and `X-Kryton-Grants` in proxy mode) give viewer/operator/admin per project; `krytonctl new-key` generates an entry.
- New docs: [docs/OBSERVABILITY.md](docs/OBSERVABILITY.md); AUTH, API, DEPLOYMENT, GA and ARCHITECTURE updated; OpenAPI extended (all three copies in sync).

### Fixed

- `scripts/deploy-remote.sh` no longer fails with `port: unbound variable` on macOS bash 3.2 when `--port` is omitted: the remote port probe now runs from a temp file instead of a heredoc inside `$( … )`.

## [1.3.0] - 2026-10-17

### Added

- Signed, 10-minute console tickets (`POST /api/v1/machines/{id}/console-ticket`) so browser consoles work with API-key auth; the dashboard renews them automatically.
- Five more Linux templates: Ubuntu 26.04 LTS, Rocky Linux 10, AlmaLinux 10, CentOS Stream 10 and Fedora 44 (eleven in total); examples regenerated.
- Six amd64 Linux cloud-image templates, typed SSH/cloud-init initialization, Linux-specific KubeVirt hardware profiles, and provider-aware UI/CLI creation.
- Native local libvirt lifecycle backend with persistent UUID records, isolated converted disks, NoCloud seeds, restart recovery, and explicit unsupported snapshot/console capabilities.
- `kryton-image` for checksum-pinned acquisition, immutable artifacts, provenance records and CDI/libvirt/cloud-init exports; generated examples and a real guest boot acceptance script.
- Tests for all Linux and Windows template profiles, real disk/seed provisioning, libvirt XML validation, and Linux template CI.
- Operator console rebuilt in React + Vite (`web/`) with the netra apple.com design system: light/dark themes, glass nav, metric band, sheet-based create flow, machine drawer with SSH hints; CI typechecks, tests and checks the embedded bundle is current.
- `deploy-remote.sh --provider` (libvirt preflight and storage setup) and `kryton-image` installation; `KRYTON_E2E_IMAGES` for the boot gate.
- `deploy-remote.sh --apikey` enables lab API-key auth in one step.
- Golden-image capture is gated on guestkit boot-readiness, and the guestkit Cutover Passport is surfaced via the API and the Images UI.
- Console login screen restyled (Apple Store chapter layout, light Zyvor orange accent) with a Sign out action that clears the session API token.
- Donation landing site and expanded docs (README, social cards, docs site).
- README, docs site and social cards now present Kryton as a Windows and Linux machine control plane with four providers; new Linux operator guide section, Linux FAQ entries, libvirt in the architecture and deployment comparisons, and Linux `initialization` in the API reference.

### Fixed

- Login screen flicker; the listen port stays configurable in the UI.
- KubeVirt DataVolume clones default to `Filesystem` volumeMode.
- Golden-image builds preserve `FINALIZE`/`IMAGE_ID` and support podman.
- Minimum Go version raised to 1.27.1 (latest stable) to address standard-library vulnerability scan findings.
- Libvirt: NoCloud seed on virtio-scsi so Debian cloud kernels (no AHCI) find it; guest IPs exclude loopback/link-local; image listings no longer re-hash artifacts.
- Libvirt domains include a VGA adapter so Debian 13 no longer boot-loops after GRUB; all eleven Linux templates pass the real boot gate.
- Boot gate: accepts Rocky/AlmaLinux point-release `VERSION_ID`, tunnels each KubeVirt SSH attempt through `virtctl port-forward --stdio`, and allows filesystem-PVC overhead in the KubeVirt disk check.
- Console proxy HTML-escapes the machine ID in its fallback page, and no longer forwards the console ticket, `Authorization` or `Cookie` to the backend.
- Pending CDI DataSources no longer appear ready to deploy; Linux images are rejected by the Windows golden installer.
- Webhook unit tests use local servers; OpenAPI copies are synchronized and CI uses the module Go version/current compatible lint tool.

## [1.2.0] - 2026-09-02

### Added

- Package-level godoc comments across every `internal/`/`cmd/` package, plus full per-method documentation on the `provider.Provider` interface.
- Comprehensive [docs/USER-GUIDE.md](docs/USER-GUIDE.md) covering all four personas (evaluator, lab operator, production operator, integrator).
- KubeVirt production setup scripts (`scripts/setup-kubevirt-production.sh`, `scripts/run-kubevirt-production-remote.sh`) automating golden-image build + CDI bootstrap end to end.
- Lab token auto-auth for the browser UI (`KRYTON_LAB_AUTO_AUTH`), so shared lab hosts don't require pasting a bearer token per session.
- Apache-2.0 license headers applied across all Go, shell, web, and OpenAPI source files (`scripts/add-license-headers.sh`).
- README table of contents, full project-layout tree, and a Development section (make targets, provider-boundary rules, CI summary).
- Helm chart `README.md` documenting `values.yaml` keys, the four values overlays, the auth-secret contract, and RBAC scope.
- Per-symbol godoc comments across `internal/`/`cmd/` exported types and functions.
- **API rate limiting** — `KRYTON_RATE_LIMIT_RPS`/`KRYTON_RATE_LIMIT_BURST` (Helm: `rateLimit.rps`/`.burst`), a per-caller token bucket keyed by API-key name (or remote address when auth is disabled); disabled by default. Returns `429`/`RATE_LIMITED`.
- **Pagination** on `GET /api/v1/machines` — `?limit=` (default 50, max 500) and `?cursor=`, returning `nextCursor` in the response envelope, matching the existing `GET /api/v1/events` pattern.
- **Helm**: `serviceMonitor` (Prometheus Operator scrape config for `/metrics`) and `podDisruptionBudget` templates, both disabled by default.
- CI: license-header check, `golangci-lint`, `govulncheck`, `gosec`, and a Trivy image scan on the multi-arch (`linux/amd64`,`linux/arm64`) image build; `.github/dependabot.yml` for Go modules, the Docker base image, and Actions.
- GitHub PR template and bug/feature issue templates under `.github/`.
- `--port <N>` flag on `scripts/deploy-remote.sh` (previously only settable via `KRYTON_PORT`).
- Expanded unit test coverage: `internal/reconciler` (0→95.5%), `internal/config` (0→87.9%), `internal/kubeapi` (0→33.6%), `internal/jobs` (0→71.6%), `internal/catalog` (0→100%), `internal/images` (0→70.7%), plus additions to `internal/api`, `internal/doctor`, and `internal/storage`.

### Fixed

- Helm chart pods failing to start under the chart's own default hardened security context: `podSecurityContext`/`containerSecurityContext` now set an explicit numeric `runAsUser`/`runAsGroup: 65532` (some kubelet/containerd versions can't verify `runAsNonRoot` against the distroless image's named `nonroot` user without one), and `KRYTON_STORAGE_CONFIG_FILE`/`KRYTON_SETTINGS_CONFIG_FILE` now point at an `emptyDir`-backed `/var/lib/kryton` so `readOnlyRootFilesystem: true` doesn't block krytond's local state writes.
- `go.mod` `gorilla/websocket` `// indirect` drift (it's imported directly, and `golang.org/x/time` for rate limiting is now a direct dependency too).
- A handful of pre-existing lint findings surfaced by the new `golangci-lint`/`gosec` CI gate: an always-first-item-only loop in `internal/doctor`'s KubeVirt feature-gate check, an unsafe `fmt.Errorf(reason)` with user-controlled content in `internal/storage`, several unchecked `Close()`/`Fprintf` errors, and one dead-code method in `internal/api`.
- Helm chart's default `image.tag` pointed at a version (`1.0.0`) that CI never actually publishes (only `latest`/`sha-<hash>` tags exist) — a default `helm install` would fail to pull. Defaults to `latest` now.

### Docs

- `docs/API.md`: pagination and rate-limiting sections.
- `docs/DEPLOYMENT.md`: observability & availability section (ServiceMonitor, PDB, rate limiting, single-replica caveat).
- `docs/ARCHITECTURE.md` / `docs/GA.md`: documented that `krytond` cannot safely run more than one replica today (TTL reconciler has no leader election; event bus is in-process).
- `CHANGELOG.md` (this file) and `deploy/helm/kryton/README.md` added.

## [1.1.0] - 2026-09-01

### Added

- **Dockur options (full)** — `spec.dockur` on create: credentials, locale, AD join, shared/OEM folders, post-install command, custom ISO, edition, audio, secure boot + TPM, extra disks, autologin.
- **`rdpUsername`** on machine responses; password redacted on GET.
- **Expanded catalog** — Windows 10/11 LTSC, Tiny11, Server 2016, Enterprise variants (12 dockur images).
- **UI** — Dockur create panel, detail **Dockur options** summary, Copy RDP, embedded noVNC console (no CDN dependency).
- **CLI** — `krytonctl create … --dockur-*` flags for all dockur fields.
- **Lab hardening** — `scripts/ensure-api-keys.sh`, `scripts/harden-lab-services.sh` (apikey auth on shared lab hosts).
- **User profile** — `deploy/helm/kryton/values-user.yaml`, [docs/USER.md](docs/USER.md).

### Fixed

- KubeVirt console iframe blocked by CSP / `X-Frame-Options`.
- VNC proxy 500 (missing `Hijacker` on response writer).
- Storage picker hang (`lsblk` timeout); Rook Ceph preferred in UI sort order.
- Actionable API errors with hints on create failures.

### Docs

- [DOCKUR.md](docs/DOCKUR.md) — full option matrix, CLI examples, auth guidance.
- OpenAPI `DockurOptions` schema on `CreateMachine`.

## [1.0.0] - Initial release

Initial public release of Kryton as a production-oriented standalone control plane.

### Added

- Stable provider-neutral machine UUIDs and explicit provider references, replacing `kubectl` / `virtctl` subprocess execution with direct Kubernetes REST API integration.
- KubeVirt state translation from `VirtualMachine.status.printableStatus` plus VMI IP discovery.
- Project RBAC, hashed API keys, trusted-proxy identity with a shared proxy secret, and secure production configuration checks.
- CloudEvents-compatible history, SSE streaming, webhooks, TTL reconciliation, structured logging, metrics, readiness, and graceful shutdown.
- Complete responsive product dashboard: Overview, Machines, Images, Activity, Settings, machine detail actions, dark/light appearance, session-token support.
- Helm RBAC, hardened container settings, OpenAPI, CLI, architecture/deployment docs, race tests, and provider integration tests.
- **Dockur lab provider** — provision real Windows guests via [dockur/windows](https://github.com/dockur/windows).
- **Doctor diagnostics** — `krytonctl doctor` and `GET /api/v1/doctor`.
- **Install progress** — `consoleUrl`, `progressPercent`, `message` on machines.
- **Remote deploy** — `scripts/deploy-remote.sh` and `make deploy-remote`.

The release source is Apache-2.0 licensed and contains no Windows media or activation material.

[Unreleased]: https://github.com/zyvorai/kryton/compare/v1.3.0...HEAD
[1.3.0]: https://github.com/zyvorai/kryton/releases/tag/v1.3.0
[1.2.0]: https://github.com/zyvorai/kryton/releases/tag/v1.2.0
[1.1.0]: https://github.com/zyvorai/kryton/releases/tag/v1.1.0
