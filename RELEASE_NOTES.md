# Kryton release notes

Narrative release write-ups. For the flat, Keep-a-Changelog-style version index, see [CHANGELOG.md](CHANGELOG.md).

## Unreleased

Kryton now runs Linux machines as well as Windows, behind the same API.

### Linux machines

- **Eleven amd64 cloud-image templates**: Ubuntu 22.04, 24.04 and 26.04 LTS; Debian 12 and 13; Rocky Linux 9 and 10; AlmaLinux 9 and 10; CentOS Stream 10; Fedora 44. Create requests take an `initialization` block (username and SSH public keys) that Kryton renders into cloud-init. Guests are password-locked, grow their root disk and install `qemu-guest-agent`. See [docs/LINUX-TEMPLATES.md](docs/LINUX-TEMPLATES.md).
- **Native libvirt provider** (`KRYTON_PROVIDER=libvirt`) for a single KVM host with no Docker or Kubernetes. Each machine gets its own converted disk and NoCloud seed, and records survive daemon restarts. Snapshots and a browser console are not offered on libvirt yet; `virsh console` works.
- **KubeVirt** runs the same templates from CDI DataSources, with Linux hardware profiles (BIOS, VirtIO, NoCloud).
- **`kryton-image`** downloads an image only against an operator-approved SHA-256, stores it by digest with a provenance manifest, and exports libvirt XML, cloud-init and CDI manifests.
- **Real boot gate**: `scripts/e2e-linux-templates.sh` creates a VM per image and checks boot, SSH, cloud-init, guest agent, disk growth and the distribution version. All eleven templates pass on a libvirt/KVM host; results are in [docs/LINUX-TEST-RESULTS.md](docs/LINUX-TEST-RESULTS.md). Getting there fixed several real defects: Debian's kernel can't see a SATA seed (now virtio-scsi), Debian 13 boot-loops without a video adapter (domains now have VGA), and guest IPs listed loopback first.

### Operator console

- Rebuilt in React + Vite with light and dark themes, a sheet-based create flow, and a machine drawer that shows the guest IP and a ready-to-copy `ssh` command.
- **Console tickets**: browser consoles now work with API-key auth. The dashboard mints a 10-minute signed ticket (`POST /api/v1/machines/{id}/console-ticket`) that only opens that machine's console, and renews it automatically. See [docs/AUTH.md](docs/AUTH.md#browser-consoles).

### Security and tooling

- Go 1.27.1 minimum, all dependencies upgraded; CI runs gofmt, vet, race tests, golangci-lint, govulncheck and gosec on every change.
- The console proxy HTML-escapes the machine ID and strips credentials before forwarding to the backend.

### Known limitation

- On KubeVirt clusters using Cilium's kube-proxy replacement, VM masquerade traffic can't reach the cluster DNS service, so Linux cloud-init stalls. Set Cilium's `bpf-lb-sock-hostns-only: true`; see [docs/KUBEVIRT.md](docs/KUBEVIRT.md#troubleshooting).

## 1.2.0

Reliability and hardening release — no breaking changes.

### Added

- **API rate limiting** — cap requests per caller with `KRYTON_RATE_LIMIT_RPS`/`KRYTON_RATE_LIMIT_BURST` (Helm: `rateLimit.rps`/`.burst`). Off by default; a token bucket keyed by API-key name (or remote address when auth is disabled).
- **Pagination** on `GET /api/v1/machines` (`?limit=`/`?cursor=`/`nextCursor`), matching the existing events endpoint's pattern.
- **Helm**: `serviceMonitor` and `podDisruptionBudget` templates (both opt-in), plus a documented single-replica caveat — see [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md).
- **CI hardening**: license-header enforcement, `golangci-lint`, `govulncheck`, `gosec`, a Trivy scan on the published image, multi-arch (`amd64`/`arm64`) builds, and Dependabot for Go/Docker/Actions.
- Extensive new unit test coverage across previously-untested packages (`reconciler`, `config`, `kubeapi`, `jobs`, `catalog`, `images`, plus more in `api`/`doctor`/`storage`).
- Full godoc coverage — every package and exported symbol now has real documentation.
- `--port <N>` flag on `scripts/deploy-remote.sh`.

### Fixed

- **Helm chart pods failing to start** under the chart's own default hardened security settings — a real bug found via live-cluster testing, not just `helm template`. Fixed with an explicit numeric `runAsUser`/`runAsGroup: 65532` and a writable `emptyDir` for krytond's local state under `readOnlyRootFilesystem: true`. If you deployed the chart before this release with default values, your pods were likely crash-looping or refusing to start — upgrade to pick up the fix.
- `go.mod` dependency-directness drift.
- A batch of latent bugs the new lint gate caught, including one real correctness bug in a KubeVirt feature-gate check that was silently only checking the first item in a list.
- Helm chart's default `image.tag` (`1.0.0`) didn't match any image CI actually publishes — `helm install` with defaults would fail to pull. Now defaults to `latest`.

### Docs

- `docs/API.md` and `docs/DEPLOYMENT.md` updated for the above; `CHANGELOG.md` and the Helm chart's own `README.md` added.

## 1.1.0

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

---

## 1.0.0

Initial public release of Kryton as a production-oriented standalone control plane.

### Major changes

- Replaced `kubectl` / `virtctl` subprocess execution with direct Kubernetes REST API integration.
- Introduced stable provider-neutral machine UUIDs and explicit provider references.
- Added KubeVirt state translation from `VirtualMachine.status.printableStatus` plus VMI IP discovery.
- Added project RBAC, hashed API keys, trusted proxy identity with a shared proxy secret, and secure production configuration checks.
- Added CloudEvents-compatible history, SSE streaming, webhooks, TTL reconciliation, structured logging, metrics, readiness, and graceful shutdown.
- Added a complete responsive product dashboard with Overview, Machines, Images, Activity, Settings, machine detail actions, dark/light appearance, and session-token support.
- Added Helm RBAC, hardened container settings, OpenAPI, CLI, architecture/deployment docs, race tests, and provider integration tests.
- **Dockur lab provider** — provision real Windows guests via [dockur/windows](https://github.com/dockur/windows).
- **Doctor diagnostics** — `krytonctl doctor` and `GET /api/v1/doctor`.
- **Install progress** — `consoleUrl`, `progressPercent`, `message` on machines.
- **Remote deploy** — `scripts/deploy-remote.sh` and `make deploy-remote`.

The release source is Apache-2.0 licensed and contains no Windows media or activation material.
