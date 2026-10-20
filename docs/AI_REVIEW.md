# AI code review (kryton)

_Generated 2026-10-20 by claude-opus-5-5 from a snapshot of tracked sources; verify before acting._

# Kryton code review

## Summary

Kryton is a Go control plane (`krytond`, `krytonctl`, `kryton-image`) that puts one REST + CloudEvents machine API in front of four backends: `demo`, `dockur` (Windows lab), `libvirt` (Linux on one KVM host) and `kubevirt` (production). It ships an embedded React/Vite operator console, a Helm chart for the KubeVirt path, many lab and bootstrap shell scripts, and an MkDocs site with marketing assets.

## Bugs and correctness risks

- **VNC proxy can hang and leak connections** (`internal/kubeapi/websocket.go`).
  - `copyWS` returns *without* sending to `errCh` on a normal close or a closed-network error.
  - The handler blocks on `<-errCh` until one side reports an error. If the client closes cleanly, the other goroutine keeps waiting on `backend.ReadMessage()`, and `ProxyVNC` never returns.
  - Fix: always signal on exit, or close both connections when the first copier exits.
- **Data race on the webhook URL** (`internal/events/events.go`).
  - `SetWebhookURL` writes `b.webhook` under `b.mu`.
  - `Publish` and `deliverWebhook` read it with no lock.
- **Webhook delivery blocks the request path** (`internal/events/events.go`).
  - Delivery runs synchronously inside `Publish`, with up to 3s per event, on every API mutation and every TTL expiry.
  - `deliverWebhook` calls `b.log.Warn` without the nil check used just above.
- **Concurrent saves can corrupt settings** (`internal/settings/store.go`).
  - `Save` releases the mutex before writing.
  - All writers share the same `path + ".tmp"`, so concurrent saves can interleave or rename a partially written file.
- **Settings cannot be cleared** (`internal/settings/store.go`, `mergeRuntime`).
  - An empty value on disk never overrides a non-empty env/initial value.
  - Clearing the webhook URL in the UI is therefore undone on restart whenever the env sets one.
- **Options that can never be false:**
  - `internal/api/storage.go`: `if !req.SetDefault { req.SetDefault = true }`.
  - `internal/api/golden.go`: `if !req.Auto { req.Auto = true }`.
  - Both fields are documented in the OpenAPI spec but cannot be turned off.
- **Probable double error response** (`internal/api/storage.go`).
  - `putStorageConfig` and `postStorageSetup` call `s.badRequest` after `decodeJSON` fails.
  - `goldenStart` just `return`s in the same situation, which suggests `decodeJSON` already writes the response. If so, these handlers write twice ("superfluous WriteHeader"). Please verify.
- **Helm RBAC does not match what krytond does:**
  - **Clone RBAC will be denied.** `internal/kubevirt/namespaces.go` (`EnsureImageCloneAccess`) creates Roles and RoleBindings. The ClusterRole in `templates/rbac.yaml` grants nothing on `rbac.authorization.k8s.io`. Binding a role that grants `datavolumes/source` also needs `bind`/`escalate` or holding that permission. Under the chart, clone RBAC setup will likely get a 403.
  - **Namespaced Role is incomplete.** With `rbac.clusterWide: false`, the Role omits `datasources`, `storageclasses`, `volumesnapshotclasses`, `namespaces` and `virtualmachineinstances/vnc`. Doctor, console and storage listing likely break in that mode.
- **Client/server provider check disagrees** (`web/src/lib/images.ts`).
  - `supportsProvider` returns `true` for `dockur` when `providers` is empty.
  - The server's `model.Image.SupportsProvider` additionally requires `DockurVersion != ""`.
  - The UI can offer images the server will reject.
- **Create form looser than the spec** (`web/src/lib/create.ts`).
  - The form accepts `disk >= 1`, but `openapi.yaml` declares `sizeGiB minimum: 16`.
  - It always sends `network: { networkId: '' }`.
- **Duplicate console tickets** (`web/src/pages/Jobs.tsx`). `JobViewer` calls `useTicketedConsole` twice for what is usually the same URL, which doubles ticket minting and refresh timers.
- **Panics on unexpected input:**
  - `internal/kubevirt/linux.go` (`applyImageProfile`) uses unchecked type assertions that panic if the VM template shape changes.
  - `internal/reconciler/ttl.go` dereferences `r.Events` and `r.Log` with no nil guard.
- **Temp credential files never cleaned up** (`internal/kubeapi/kubeconfig.go`). `writeTempPEM` leaves the CA, client cert and **client key** in `$TMPDIR` permanently, and does so again on every load.
- **Missing assets return HTML** (`internal/api/static.go`). A missing `/assets/*.js` falls back to `index.html` with a 200 and `text/html`. That hides deploy skew instead of returning a 404.

## Security concerns

- **The lab "hardening" effectively removes auth.**
  - `scripts/harden-lab-services.sh` sets `KRYTON_LAB_AUTO_AUTH=true`, `KRYTON_ALLOW_INSECURE=true` and `KRYTON_CORS_ORIGINS=*` on services bound to the public host IP.
  - `internal/api/lab.go` then hands the raw **admin, `projects:["*"]`** token to any unauthenticated caller of `/api/v1/lab/bootstrap`.
  - With CORS `*`, any website a lab user visits may be able to read it; this depends on the CORS middleware, which isn't shown.
- **Cross-site WebSocket hijacking risk on VNC.**
  - `vncUpgrader.CheckOrigin` returns `true` unconditionally.
  - Console tickets are also delivered as an HttpOnly cookie (per `openapi.yaml`), so a cross-origin page could open the VNC socket if the cookie's SameSite setting allows it (not visible here).
  - Origin should be checked against the host and `KRYTON_CORS_ORIGINS`.
- **Missing project scope on destructive storage actions** (`internal/api/storage.go`).
  - `putStorageConfig` and `postStorageSetup` only check `Role ∈ {Admin, Operator}`, not project membership.
  - An operator scoped to a single project can change the cluster-wide StorageClass. They can also start a Rook install with `wipeDevice: true` on a host device.
  - Wiping should require Admin.
- **SSRF via the webhook URL.** `eventWebhookUrl` in settings drives arbitrary server-side POSTs. There is no scheme or host validation (where `PUT /api/v1/settings` is handled isn't shown).
- **Possible XSS in the console viewer** (`cmd/krytond/web/console-viewer.js`). `show()` writes `bootError` and exception text through `innerHTML`. If `bootError` carries provider or Kubernetes error text, that is an injection vector; use `textContent`.
- **Overly broad clone grant** (`deploy/kubevirt/clone-rbac.yaml` and `ensureCloneRoleBinding`). Clone rights go to the `default` ServiceAccount of each project namespace, so any pod there can clone golden images.
- **Script hygiene:**
  - `scripts/ensure-api-keys.sh` writes the token before `chmod 600`, with no `umask 077`.
  - `scripts/e2e-kryton-golden-host.sh` interpolates `${id}` directly into inline Python.
  - `zyvor-rbd-prod` is hardcoded in both `harden-lab-services.sh` and the e2e script.
- **File path leak.** `goldenPassport` echoes the on-disk path in its 404 message.
- **Lab chart contradicts the GA rule.** `values-lab.yaml` runs `provider: kubevirt` with `auth.mode: disabled` and `allowInsecure: true`, while GA.md and the README claim "auth never off on kubevirt". Either the claim or the overlay is wrong.

## Test and CI gaps

- **Helm chart:** nothing in CI runs `helm lint` or `helm template`. This matters given the RBAC gaps above and the past crash-loop bug noted in the CHANGELOG.
- **Image scan ordering** (`ci.yml`): Trivy runs *after* `push: true` to `latest`, so a vulnerable image is already published when the scan fails. The `image` job also does not depend on the `web` job.
- **Race detection:** `go test -race` only runs in `linux-templates.yml`; the main `test` job runs without it, yet there are real races (see above).
- **Untested paths:**
  - No tests for `ProxyVNC`, settings concurrency, `harden-lab-services.sh`, or the CORS + lab-bootstrap interaction.
  - No test asserting storage setup is denied to project-scoped operators.
  - `TestLabBootstrapEnabled` asserts the token leak is *working*, with nothing checking it is gated.
- **Docs checks:**
  - `docs.yml` runs only on push to `main`, so there is no PR-time `mkdocs build --strict`.
  - `docs/scripts/check_no_hex_surfaces.py` is never invoked.
- **Shell scripts:** only `bash -n` runs on one script; there is no `shellcheck`.
- **Pinned toolchain:** govulncheck pins `go-version-input: "1.27.1"` instead of reading `go.mod`.
- **OpenAPI drift:** nothing checks the spec against registered routes. `/api/v1/lab/bootstrap` is undocumented, and the `integrations` tag is used but not declared.

## Documentation gaps

- **Repo and module naming is inconsistent.**
  - The module is `github.com/zyvorai/kryton`, but the README clones `zyvor-kryton.git` and then runs `cd kryton`, which fails.
  - `SECURITY.md`, `FAQ.md`, `docs/index.md` and the CHANGELOG compare links point at `zyvorai/kryton`, while the badges and mkdocs point at `zyvor-kryton`.
- **Missing values file.** The Helm README, `setup-kubevirt-production.sh` and USER.md reference `values-user.yaml`, but only `values-customer.yaml` appears in the provided files. It may simply be absent from this excerpt; confirm.
- **Lab token path is inconsistent.**
  - `Login.tsx` says `ensure-api-keys.sh` writes `/etc/kryton/lab.token`.
  - The script actually writes `~/.kryton/lab.token`.
  - DEPLOY-REMOTE uses `/etc/kryton/lab.token` for `--apikey`.
- **Stale version and positioning:**
  - The `openapi.yaml` version (`1.1.0`) and description ("Windows virtualization") disagree with the chart (`1.2.0`) and the Linux support.
  - `Chart.yaml` and `docs/ATLAS.md` still say Windows-only.
- **Stale marketing assets:**
  - `docs/assets/social-preview.html` says "Go 1.23+" and "three providers".
  - `docs/client-presentations/*` describe v1.2.0 with Windows only.
  - `docs/ux/readme-*.html` use the old orange mark, despite the claim in `docs/social/README.md` that the old palette was "fully replaced".
- **Incomplete feature lists:** the issue templates and CONTRIBUTING list only demo/dockur/kubevirt, with no libvirt.
- **Configuration reference missing:** the README says "full table in prior docs or `internal/config`". There is no env-var reference page.
- **Undocumented risk:** AUTH.md and DOCKUR.md should state explicitly that lab auto-auth exposes an admin token.

## Top 5 recommended actions (ordered)

1. **Lock down lab auto-auth.**
   - Restrict `/api/v1/lab/bootstrap` to loopback, or issue a viewer-scoped token.
   - Remove `KRYTON_C
