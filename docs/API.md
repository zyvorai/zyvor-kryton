---
hero:
  eyebrow: API
  title: API reference
---

Kryton exposes a versioned REST API under `/api/v1`. All machine operations are scoped to a **project**. Authentication uses bearer tokens (`KRYTON_TOKEN`) or trusted proxy headers when configured.

**How to get a token:** [AUTH.md](AUTH.md) (`./scripts/ensure-api-keys.sh` → `cat ~/.kryton/lab.token`, or `krytonctl generate-token` for Helm/CI).

OpenAPI spec: [`openapi.yaml`](https://github.com/zyvorai/kryton/blob/main/openapi.yaml).

---

## Endpoints

```text
GET    /api/v1                          # discovery catalog (public)
GET    /api/v1/me
GET    /api/v1/projects
GET    /api/v1/capabilities
GET    /api/v1/doctor
GET    /api/v1/storage
GET    /api/v1/storage/config
PUT    /api/v1/storage/config
GET    /api/v1/storage/setup
POST   /api/v1/storage/setup
GET    /api/v1/settings
PUT    /api/v1/settings
POST   /api/v1/settings/test
POST   /api/v1/integrations/atlas/test
GET    /api/v1/images
GET    /api/v1/golden
POST   /api/v1/golden
GET    /api/v1/golden/{id}
GET    /api/v1/golden/{id}/passport      # guestkit Cutover Passport JSON, 404 if none recorded
POST   /api/v1/golden/{id}/bootstrap
GET    /api/v1/jobs
GET    /api/v1/jobs/{id}
GET    /api/v1/summary?project=<project>
GET    /api/v1/machines?project=<project>&limit=<n>&cursor=<opaque>&selector=<expr>
POST   /api/v1/machines                  # optional Idempotency-Key header
POST   /api/v1/machines/actions          # bulk start/stop by ids or selector
GET    /api/v1/machines/{id}?project=<project>
GET    /api/v1/machines/{id}/console?project=<project>
POST   /api/v1/machines/{id}/console-ticket?project=<project>
GET    /api/v1/machines/{id}/vnc?project=<project>
POST   /api/v1/machines/{id}/start?project=<project>
POST   /api/v1/machines/{id}/stop?project=<project>
POST   /api/v1/machines/{id}/snapshot?project=<project>
GET    /api/v1/machines/{id}/snapshots?project=<project>
POST   /api/v1/machines/{id}/snapshots/{sid}/restore?project=<project>
DELETE /api/v1/machines/{id}/snapshots/{sid}?project=<project>
DELETE /api/v1/machines/{id}?project=<project>
PUT    /api/v1/machines/{id}/ttl?project=<project>   # extend / clear TTL
GET    /api/v1/events?type=&machineId=&since=&limit=
GET    /api/v1/events/stream
GET    /api/v1/audit                     # admin; filters + cursor pagination
GET    /api/v1/webhooks?project=<project>
POST   /api/v1/webhooks
GET    /api/v1/webhooks/{id}
PUT    /api/v1/webhooks/{id}
DELETE /api/v1/webhooks/{id}
POST   /api/v1/webhooks/{id}/test
GET    /api/v1/webhooks/deliveries?project=<project>&status=&subscriptionId=
GET    /api/v1/webhooks/deliveries/{id}
POST   /api/v1/webhooks/deliveries/{id}/redeliver
GET    /api/v1/webhooks/dead-letters?project=<project>
GET    /api/v1/schedules?project=<project>&machineId=
POST   /api/v1/schedules
GET    /api/v1/schedules/{id}
PUT    /api/v1/schedules/{id}
DELETE /api/v1/schedules/{id}
GET    /api/v1/quotas
GET    /api/v1/quotas/{project}
PUT    /api/v1/quotas/{project}
DELETE /api/v1/quotas/{project}
```

Public (no auth): `GET /api/v1` · `GET /openapi.yaml` · `GET /healthz` · `GET /readyz`. `GET /metrics` is open only with auth disabled or `KRYTON_METRICS_PUBLIC=true`; otherwise it needs the metrics token or an API key. The audit, webhook, schedule, TTL and quota endpoints are documented in [OBSERVABILITY.md](OBSERVABILITY.md).

OpenAPI 3.1: [`openapi.yaml`](https://github.com/zyvorai/kryton/blob/main/openapi.yaml) — also served live at `/openapi.yaml`.

---

## Fleet operations

**Label selectors.** Machines carry `spec.labels` (set at create, at most 64 labels; keys `A-Za-z0-9_.:/-` up to 128 characters, values up to 256). `GET /api/v1/machines?selector=...` filters on them before pagination. The grammar is a subset of the Kubernetes label selector, comma-separated and ANDed: `env=ci`, `env!=prod`, `env in (ci,qa)`, `env notin (prod)`, `owner` (key exists), `!owner` (key absent). `!=` and `notin` also match machines that lack the key. A selector is at most 1024 bytes, 16 requirements and 16 values per set; anything else is `400 INVALID_REQUEST`.

```bash
krytonctl create --label env=ci --label team=core ci-01
krytonctl list --selector 'env=ci,!pinned'
```

**Idempotent create.** Send `Idempotency-Key: <1-128 chars of A-Za-z0-9_.:->` on `POST /machines` (the quoted Structured Field form is accepted too). The first successful result is remembered for 24 hours per caller, project and key. A retry with the same body returns the original `201` with `Idempotent-Replay: true` and creates nothing; a retry with a different body is `422 IDEMPOTENCY_KEY_REUSED`; a retry while the first request is still running is `409 IDEMPOTENCY_IN_PROGRESS`. Failed creates are not remembered, so they can be retried with the same key. Limits worth knowing: keys live in memory (a `krytond` restart forgets them), the store holds at most 10,000 keys (`503 IDEMPOTENCY_STORE_FULL` beyond that), and a replay never repeats the one-time `generatedPassword`. Follows the IETF `Idempotency-Key` draft.

```bash
krytonctl create --idempotency-key order-8841 --label env=ci ci-01
```

**Bulk start/stop.** `POST /api/v1/machines/actions` takes `{"project","action":"start|stop","ids":[...]}` or `{"action","selector":"env=ci"}` (exactly one of `ids` and `selector`, at most 100 machines; a selector that matches more is rejected). Operator role is required. The response is `200` with `total`, `succeeded`, `failed` and a per-machine `results` list (`ok`, `state`, or an `error` with `code`), so one missing machine does not hide the rest. Each success emits the normal started/stopped CloudEvent. Bulk delete is intentionally not offered.

```bash
krytonctl bulk stop --selector 'env=ci'
krytonctl bulk start 3f2c... 9a41...
```

**Event filters.** `GET /api/v1/events` accepts `type` (comma-separated, at most 8, a trailing `*` matches a prefix such as `io.kryton.machine.*`), `machineId` and `since` (RFC 3339), applied before `limit`. The window searched is the in-memory ring of the last 500 events.

```bash
krytonctl events --type 'io.kryton.machine.*' --machine 3f2c...
```

---

## Integrating from other projects

Kryton is designed for Zyvor products (Axiom, Haven, automation) to drive Windows VMs over HTTP.

```bash
# 1. Discover the surface (no token required)
curl -sS "$KRYTON_URL/api/v1" | jq .

# 2. Fetch OpenAPI for codegen
curl -sS "$KRYTON_URL/openapi.yaml" -o kryton.openapi.yaml

# 3. Call authenticated APIs
curl -sS -H "Authorization: Bearer $KRYTON_TOKEN" \
  "$KRYTON_URL/api/v1/machines?project=default"
```

**CORS** — set `KRYTON_CORS_ORIGINS` to the calling app origins (comma-separated), or `*` in lab:

```bash
Environment=KRYTON_CORS_ORIGINS=https://axiom.example,https://haven.example
```

**Auth for service-to-service:**

| Mode | How |
|------|-----|
| `apikey` | `Authorization: Bearer <token>` — create with [AUTH.md](AUTH.md); hash with `krytonctl hash-token` for `keys.json` |
| `proxy` | Edge sets `X-Kryton-User`, `X-Kryton-Role`, `X-Kryton-Projects`, `X-Kryton-Proxy-Secret` |
| `disabled` | Lab only (`KRYTON_ALLOW_INSECURE=true`) |

```bash
# Lab shortcut
export KRYTON_TOKEN=$(cat ~/.kryton/lab.token)
# or: export KRYTON_TOKEN=$(ssh user@host 'cat ~/.kryton/lab.token')
```

**Minimal create flow:**

```bash
curl -sS -X POST "$KRYTON_URL/api/v1/machines" \
  -H "Authorization: Bearer $KRYTON_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "project": "default",
    "name": "win-app01",
    "image": "windows-11-enterprise",
    "compute": {"cpu": 4, "memoryMiB": 8192},
    "disk": {"sizeGiB": 80, "storageClass": "rook-ceph-block"}
  }'
```

Watch progress via `GET /api/v1/jobs` or `GET /api/v1/events/stream` (SSE).

CLI equivalent: `KRYTON_URL=… KRYTON_TOKEN=… krytonctl …`.

---

## Projects

`GET /api/v1/projects` returns the configured project list and default project.

---

## Capabilities

Browser consoles (iframes, new tabs, websockets) can't send an `Authorization` header. `POST /api/v1/machines/{id}/console-ticket` (viewer role) returns a 10-minute signed ticket; append it as `?console_ticket=` to that machine's `/console` or `/vnc` URL. Krytond also sets it as an HttpOnly, `SameSite=Strict` cookie scoped to `/api/v1/machines/{id}/` so console sub-resources and reconnects keep working. A ticket only grants `GET` on that one machine's console paths, and every ticket is invalidated when krytond restarts.

`GET /api/v1/capabilities` reports what the active provider supports (start/stop, snapshots, console, etc.). Use this to build provider-aware UIs without hard-coding behavior.

---

## Doctor

`GET /api/v1/doctor` runs environment diagnostics and returns a structured report:

```json
{
  "healthy": true,
  "provider": "dockur",
  "findings": [
    {"check": "auth", "status": "pass", "message": "Auth mode apikey"},
    {"check": "kvm", "status": "pass", "message": "/dev/kvm is available"}
  ]
}
```

Finding `status` values: `pass` · `warn` · `fail`. CLI equivalent: `krytonctl doctor`.

---

## Images

`GET /api/v1/images` returns the catalog. Each entry includes minimum CPU/memory, disk size, and (for dockur) a `dockurVersion` mapping.

---

## Machines

### List and get

`GET /api/v1/machines?project=default` returns machines in the project, paginated:

| Param | Default | Notes |
|-------|---------|-------|
| `limit` | 50 | Max items per page, capped at 500 |
| `cursor` | *(none)* | Opaque value from the previous response's `nextCursor`; omit for the first page |

```json
{
  "items": [ { "id": "…", "…": "…" } ],
  "nextCursor": "MzY5Y2E3MzctOGJmYy00YTY0LWEyNGUtYzJhMzIwMTgyNzFh"
}
```

`nextCursor` is absent on the last page. Machines are ordered by ID for a stable sort across pages — the provider itself gives no ordering guarantee.

`GET /api/v1/machines/{id}?project=default` returns a single machine.

### Machine object

| Field | Type | Description |
|-------|------|-------------|
| `id` | string | Stable Kryton UUID |
| `project` | string | Project scope |
| `provider` | string | `demo` · `dockur` · `kubevirt` |
| `state` | string | Lifecycle state |
| `spec` | object | Desired configuration |
| `providerRef` | object | Provider-specific reference |
| `ipAddresses` | string[] | Guest IPs when known |
| `consoleUrl` | string | Web console URL (dockur/demo) |
| `progressPercent` | int | Install progress 0–100 |
| `message` | string | Human-readable status |
| `conditions` | array | Structured conditions |
| `createdAt` / `updatedAt` | timestamp | Audit timestamps |
| `expiresAt` | timestamp | TTL expiry when set |

### Create

`POST /api/v1/machines`

```json
{
  "project": "default",
  "name": "finance-win01",
  "image": "windows-server-2025",
  "compute": {"cpu": 4, "memoryMiB": 8192},
  "disk": {"sizeGiB": 80},
  "network": {"networkId": ""},
  "ttlMinutes": 0
}
```

Validate CPU and memory against the image catalog minimums. Names must be DNS-style labels (≤ 63 characters).

Linux templates (`ubuntu-26.04`, `ubuntu-24.04`, `ubuntu-22.04`, `debian-13`, `debian-12`, `rocky-10`, `rocky-9`, `almalinux-10`, `almalinux-9`, `centos-stream-10`, `fedora-44`) take the same body plus an optional `initialization` block, rendered into cloud-init. Guests are password-locked, so supply at least one key to log in:

```json
{
  "project": "default",
  "name": "linux-dev",
  "image": "ubuntu-24.04",
  "compute": {"cpu": 2, "memoryMiB": 2048},
  "disk": {"sizeGiB": 20},
  "initialization": {
    "username": "ubuntu",
    "sshAuthorizedKeys": ["ssh-ed25519 AAAA... you@laptop"]
  }
}
```

Linux machines run on the `libvirt` or `kubevirt` provider; see [LINUX-TEMPLATES.md](LINUX-TEMPLATES.md).

### Lifecycle actions

| Action | Method | Path |
|--------|--------|------|
| Start | POST | `/api/v1/machines/{id}/start?project=…` |
| Stop | POST | `/api/v1/machines/{id}/stop?project=…` |
| Snapshot | POST | `/api/v1/machines/{id}/snapshot?project=…` |
| List snapshots | GET | `/api/v1/machines/{id}/snapshots?project=…` |
| Restore snapshot | POST | `/api/v1/machines/{id}/snapshots/{sid}/restore?project=…` |
| Delete snapshot | DELETE | `/api/v1/machines/{id}/snapshots/{sid}?project=…` |
| Delete | DELETE | `/api/v1/machines/{id}?project=…` |

### dockur guest management

Only with `KRYTON_PROVIDER=dockur` (other providers answer `501 UNSUPPORTED`). Full semantics, limits and the guest-agent protocol are in [DOCKUR.md](DOCKUR.md).

| Action | Method | Path |
|--------|--------|------|
| Graceful restart | POST | `/api/v1/machines/{id}/restart` |
| Readiness probe (200/503) | GET | `/api/v1/machines/{id}/ready` |
| Container log | GET | `/api/v1/machines/{id}/logs` |
| Shared folder list / get / put / delete | GET, GET, PUT, DELETE | `/api/v1/machines/{id}/shared[/{path}]` |
| Submit / list guest jobs | POST, GET | `/api/v1/machines/{id}/jobs` |
| Job status / cancel | GET, POST | `/api/v1/machines/{id}/jobs/{jid}[/cancel]` |
| Job output / artifacts | GET | `/api/v1/machines/{id}/jobs/{jid}/logs`, `/artifacts[/{path}]` |

The create response carries `generatedPassword` once when no `dockur.password` was supplied. New error codes: `PAYLOAD_TOO_LARGE` (413) and `PRECONDITION_FAILED` (409). New events: `io.kryton.machine.restarted`, `.ready`, `.shared.uploaded`, `.job.submitted`, `.job.completed`.

---

## Events

`GET /api/v1/events` returns recent CloudEvents history.

`GET /api/v1/events/stream` opens an SSE stream for live lifecycle updates.

Common event types:

| Type | When |
|------|------|
| `io.kryton.machine.created` | Machine record created |
| `io.kryton.machine.install.started` | Dockur install begins |
| `io.kryton.machine.started` | Machine running |
| `io.kryton.machine.stopped` | Machine stopped |
| `io.kryton.snapshot.created` | Snapshot requested |
| `io.kryton.snapshot.restored` | Restore requested |
| `io.kryton.snapshot.deleted` | Snapshot deleted |
| `io.kryton.machine.expiring` | Machine is within `KRYTON_TTL_WARN` of its TTL expiry |
| `io.kryton.machine.ttl.updated` | TTL extended or cleared |
| `io.kryton.machine.restarted` | Scheduled restart completed |
| `io.kryton.schedule.created` / `.deleted` / `.executed` | Scheduled action lifecycle and runs |
| `io.kryton.quota.updated` | Project quota set or removed |

---

## Errors

All errors use a consistent envelope:

```json
{
  "error": {
    "code": "INVALID_REQUEST",
    "message": "name must be a DNS-style label up to 63 characters",
    "requestId": "..."
  }
}
```

Common codes: `INVALID_REQUEST` · `NOT_FOUND` · `FORBIDDEN` · `CONFLICT` · `INTERNAL_ERROR` · `RATE_LIMITED` · `QUOTA_EXCEEDED` (403 or 409 on create, see [quotas](OBSERVABILITY.md#project-quotas)) · `WEBHOOK_URL_BLOCKED` · `WEBHOOK_LIMIT` · `SCHEDULE_LIMIT` · `NOT_CONFIGURED` (501).

---

## Rate limiting

Set `KRYTON_RATE_LIMIT_RPS` (and optionally `KRYTON_RATE_LIMIT_BURST`) to cap requests per caller — a token bucket keyed by API-key name (or remote address when auth is disabled). Disabled by default (`KRYTON_RATE_LIMIT_RPS=0`). Exceeding the limit returns `429` with `error.code: "RATE_LIMITED"`; back off and retry.

---

## Authentication

**API key mode** — send `Authorization: Bearer <token>`.

**Proxy mode** — the reverse proxy sets:

- `X-Kryton-User`
- `X-Kryton-Role` (`viewer` · `operator` · `admin`)
- `X-Kryton-Projects` (comma-separated)
- `X-Kryton-Proxy-Secret` (shared secret)

Roles are always intersected with project scope.
