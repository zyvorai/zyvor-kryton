---
hero:
  eyebrow: OPERATIONS
  title: Operations and observability
---

Everything an operator needs to watch, audit and keep a Kryton control plane tidy: Prometheus metrics, an append-only audit log, signed outbound webhooks, scheduled power actions with TTL hygiene, and per-project quotas. Access control for all of it is described in [AUTH.md](AUTH.md#roles-and-projects).

| Feature | Endpoint(s) | Minimum role |
|---------|-------------|--------------|
| [Metrics](#prometheus-metrics) | `GET /metrics` | metrics token, or any API key (own projects) |
| [Audit log](#audit-log) | `GET /api/v1/audit` | admin |
| [Webhooks](#outbound-webhooks) | `/api/v1/webhooks…` | read: operator, write: admin |
| [Schedules](#scheduled-power-actions) | `/api/v1/schedules…` | read: viewer, write: operator |
| [TTL](#machine-ttl-and-auto-delete) | `PUT /api/v1/machines/{id}/ttl` | operator |
| [Quotas](#project-quotas) | `/api/v1/quotas…` | read: viewer, write: admin |

State for webhooks, schedules and quotas is kept in JSON files (default `~/.kryton/webhooks.json`, `schedules.json`, `quotas.json`; the Helm chart points them at the `state` volume). Back those files up, or mount a persistent volume, if you need them to survive pod replacement.

---

## Prometheus metrics

`GET /metrics` serves Prometheus text format (v0.0.4). No client library is involved.

### Who may scrape

| Situation | Behaviour |
|-----------|-----------|
| `KRYTON_AUTH_MODE=disabled` | Open (lab/demo) |
| `KRYTON_METRICS_PUBLIC=true` | Open, even with auth on (keep it behind a NetworkPolicy) |
| `KRYTON_METRICS_TOKEN` (or `KRYTON_METRICS_TOKEN_FILE`) set | `Authorization: Bearer <metrics token>` gives the full view |
| Otherwise | Any valid API key or proxy identity; per-project series are limited to projects that identity can view. `401` without credentials |

The dedicated token is separate from API keys, so Prometheus never holds a credential that can create or delete machines. Generate one with `krytonctl generate-token`.

> **Upgrade note.** Earlier releases served `/metrics` unauthenticated even with API keys enabled. With auth on, you must now set a metrics token (recommended) or `KRYTON_METRICS_PUBLIC=true` to keep an existing scraper working.

### Series

| Metric | Type | Labels |
|--------|------|--------|
| `kryton_machines` | gauge | `project`, `provider`, `state` (every state is emitted, zero included) |
| `kryton_provider_up` | gauge | `provider` (1 when the health check passes) |
| `kryton_api_requests_total` | counter | `method`, `route` (template such as `/api/v1/machines/{id}`), `code` |
| `kryton_http_request_duration_seconds` | histogram | `method`, `route`; buckets 5 ms … 10 s |
| `kryton_http_requests_total`, `kryton_http_errors_total` | counter | whole-process totals |
| `kryton_operations_total` | counter | `operation` (create, start, stop, snapshot, delete, ttl-update …) |
| `kryton_rate_limit_rejections_total` | counter | requests answered `429` |
| `kryton_auth_failures_total` | counter | `/api/*` requests answered `401` |
| `kryton_quota_limit` / `kryton_quota_usage` | gauge | `project`, `resource` (`machines`, `vcpu`, `memory_mib`, `disk_gib`); the limit series is absent when unlimited |
| `kryton_quota_rejections_total` | counter | `project`, `resource` |
| `kryton_webhook_deliveries_total` | counter | `project`, `outcome` (`success`, `failure`, `dead`) |
| `kryton_webhook_dead_letters` | gauge | `project` |
| `kryton_scheduled_actions_total` | counter | `action`, `outcome` |
| `kryton_ttl_expirations_total` | counter | `project` |
| `kryton_audit_entries` | gauge | entries held in memory |

Route labels are templates, never raw paths, so cardinality stays bounded. Machine and provider state is read at most once per 10 seconds regardless of scrape frequency; the Prometheus staleness window is therefore the only lag you will see.

### Helm and ServiceMonitor

```bash
kubectl -n kryton create secret generic kryton-metrics \
  --from-literal=token="$(krytonctl generate-token)"
helm upgrade --install kryton ./deploy/helm/kryton -n kryton \
  --set serviceMonitor.enabled=true \
  --set metrics.tokenSecret.name=kryton-metrics
```

The chart injects the secret as `KRYTON_METRICS_TOKEN` and the `ServiceMonitor` presents it through `endpoints[].authorization`, so scraping works out of the box once the Prometheus Operator CRDs are installed. Without a token secret the ServiceMonitor scrapes unauthenticated, which only works when `auth.mode=disabled` or `metrics.public=true`.

### Useful alerts

```yaml
- alert: KrytonProviderDown
  expr: kryton_provider_up == 0
  for: 5m
- alert: KrytonWebhookDeadLetters
  expr: kryton_webhook_dead_letters > 0
- alert: KrytonQuotaNearlyFull
  expr: kryton_quota_usage / on(project, resource) kryton_quota_limit > 0.9
- alert: KrytonSlowAPI
  expr: histogram_quantile(0.95, sum by (le, route) (rate(kryton_http_request_duration_seconds_bucket[5m]))) > 2
```

CLI: `krytonctl metrics` (uses `KRYTON_METRICS_TOKEN` if set, else `KRYTON_TOKEN`).

---

## Audit log

An append-only record of every **mutating** API call (`POST`, `PUT`, `PATCH`, `DELETE`) and of background actions taken by Kryton itself. Reads are not logged. There is no API to edit or delete entries.

Each entry:

| Field | Meaning |
|-------|---------|
| `time`, `seq`, `id` | when; monotonically increasing sequence; unique id |
| `actor`, `role` | API-key name or proxy user (`anonymous` for a failed login, `system:scheduler`, `system:ttl` for background work); `per-project` for grants-only keys |
| `project`, `machineId` | what was touched, when applicable |
| `action` | stable name such as `machine.create`, `snapshot.restore`, `settings.update`, `webhook.create`, `schedule.run`, `machine.expire` |
| `method`, `path`, `status`, `result` | HTTP detail; `result` is `success`, `denied` (401/403/429) or `error` |
| `requestId`, `remote` | correlate with server logs (`X-Request-ID`) and the caller address |
| `detail` | extra context, for example the quota that blocked a create |

Rejected calls are recorded too (a 403 or 429 is exactly what you want to see), but request bodies, tokens and webhook secrets never are.

### Query

```bash
krytonctl audit --action 'machine.*' --result denied --since 2026-11-01T00:00:00Z --limit 100
curl -H "Authorization: Bearer $KRYTON_TOKEN" \
  "$KRYTON_URL/api/v1/audit?project=lab&actor=ci&limit=50"
```

Filters: `project`, `actor`, `action` (exact, or a prefix ending in `*`), `machineId`, `result`, `since`, `until` (RFC 3339). Results are newest first; pass `nextCursor` back as `cursor` for the next page. The limit is 1-500 (default 50).

Authorization: the caller needs `admin` on at least one project and sees only entries for projects it administers; entries without a project (for example a failed login) need admin on `*`. A project admin asking for another project gets `403`.

### Retention and the file sink

| Variable | Default | Meaning |
|----------|---------|---------|
| `KRYTON_AUDIT_MAX_ENTRIES` | `10000` | In-memory entries served by the API (oldest dropped) |
| `KRYTON_AUDIT_FILE` | unset | Also append JSON lines to this file (mode `0600`) |
| `KRYTON_AUDIT_MAX_FILE_BYTES` | `10485760` | Rotate when the file would exceed this size |
| `KRYTON_AUDIT_MAX_FILES` | `5` | Rotated files kept: `audit.jsonl.1` (newest) … `.N` |

Without a file sink the log lives in memory only and is lost on restart; set `KRYTON_AUDIT_FILE` when you need durable history (the API reloads the active and newest rotated file at startup). Ship the JSONL to your log pipeline for long-term retention; an unauthenticated flood of bad requests can push older entries out of the in-memory window, but never out of the file sink until rotation limits are reached.

---

## Outbound webhooks

Per-project subscriptions that receive Kryton's [CloudEvents](API.md#events) over HTTPS. This is in addition to the single global `KRYTON_EVENT_WEBHOOK_URL` sink, which is unchanged.

### Create

```bash
krytonctl webhooks create https://ci.example.com/kryton \
  --project lab --event 'io.kryton.machine.*' --description "CI notifier"
```

```json
{ "id": "…", "project": "lab", "url": "https://ci.example.com/kryton",
  "eventTypes": ["io.kryton.machine.*"], "enabled": true,
  "secret": "whsec_…" }
```

The **`secret` is shown only in this response**; store it. To rotate, create a new subscription and delete the old one. `eventTypes` are glob patterns (empty list or `*` = every event). A subscription only ever receives events whose `data.project` equals its own project. Limits: 50 subscriptions per project, 20 patterns each. Admin on the project is required to create, change, delete, test or redeliver; operators can read.

### Delivery format and signature

`POST` with `Content-Type: application/cloudevents+json` and the CloudEvent as the body, plus:

| Header | Value |
|--------|-------|
| `X-Kryton-Event-ID`, `X-Kryton-Event-Type` | CloudEvent `id` and `type` |
| `X-Kryton-Delivery-ID` | stable across retries; use it for idempotency |
| `X-Kryton-Attempt` | 1, 2, 3 … |
| `X-Kryton-Timestamp` | unix seconds when the attempt was made |
| `X-Kryton-Signature` | `sha256=` + hex `HMAC-SHA256(secret, timestamp + "." + body)` |

Verify the signature over the raw body and reject stale timestamps (for example older than 5 minutes) to stop replays:

```python
import hmac, hashlib, time
def verify(secret, ts, body, header):
    if abs(time.time() - int(ts)) > 300:
        return False
    want = "sha256=" + hmac.new(secret.encode(), f"{ts}.".encode() + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(want, header)
```

(This signs the timestamp as well as the body, unlike the legacy global webhook's `X-Kryton-Signature`, which signs the body only.)

### Retries, delivery log and dead letters

Any `2xx` is success. Anything else (other status, timeout, connection error) is retried with exponential backoff: after 1 s, 5 s, 30 s, 2 min and 10 min, so six attempts in total. A delivery that exhausts them becomes `dead`.

| Status | Meaning |
|--------|---------|
| `pending` | queued for its first attempt |
| `retrying` | failed, next attempt scheduled (`nextAttemptAt`) |
| `succeeded` | a `2xx` was received |
| `dead` | out of retries (dead-letter list) |

```bash
krytonctl webhooks deliveries --project lab --status retrying
krytonctl webhooks dead-letters --project lab
krytonctl webhooks redeliver <delivery-id>     # fresh round of retries
krytonctl webhooks test <webhook-id>           # sends io.kryton.webhook.test
```

The log keeps the last 500 deliveries (oldest finished ones are evicted first; in-flight ones never are). The detail endpoint `GET /api/v1/webhooks/deliveries/{id}` returns the event payload and the last 10 attempts (time, status code, error, duration). The log and pending retries survive restarts. Deleting a subscription stops its queued deliveries.

### SSRF guard

A webhook URL must be:

- **`https`** (plain `http` is refused), with no `user:pass@` part;
- resolving only to **public** addresses. Loopback, RFC 1918 private, link-local (including the `169.254.169.254` cloud metadata address), CGNAT `100.64.0.0/10`, ULA `fc00::/7`, multicast, unspecified and documentation/reserved ranges are refused.

The check runs when the subscription is created or changed **and again at connection time on the resolved address**, so DNS rebinding cannot swap in an internal address afterwards. Redirects are not followed and environment proxies are ignored. To deliver to an internal receiver, an operator (not an API caller) allowlists it:

```bash
KRYTON_WEBHOOK_ALLOW_HOSTS="10.20.0.0/16,hooks.internal.example"
```

Entries are CIDRs, single IPs or hostnames. `https` is still required.

---

## Scheduled power actions

Run `start`, `stop`, `restart` (stop then start) or `snapshot` on a machine on a cron schedule, evaluated in an IANA timezone.

```bash
# Stop the lab VM every weekday at 19:00 Berlin time, snapshot it nightly.
krytonctl schedules create "$ID" --action stop --cron "0 19 * * mon-fri" --tz Europe/Berlin
krytonctl schedules create "$ID" --action snapshot --cron "30 2 * * *" --tz UTC --snapshot-name nightly
krytonctl schedules list
krytonctl schedules disable <schedule-id>
```

Cron has five fields (`minute hour day-of-month month day-of-week`); each accepts `*`, numbers, ranges (`1-5`), lists (`1,15`) and steps (`*/10`, `10-50/10`). Month and weekday accept three-letter names (`jan`, `mon-fri`); `0` and `7` are Sunday. `@hourly`, `@daily`, `@weekly`, `@monthly` and `@yearly` are supported. As in classic cron, when both day-of-month and day-of-week are restricted a time matches if either does.

Behaviour worth knowing:

- **Timezones follow wall-clock time.** `0 9 * * *` in `America/New_York` is 14:00 UTC in winter and 13:00 UTC in summer. A time skipped by a spring-forward transition does not run that day; a repeated hour during fall-back runs once.
- **No replay after downtime.** The scheduler polls every 15 s. A run that starts more than 5 minutes late (for example after krytond was down) is recorded as `skipped` and rescheduled rather than replayed, so a restart never fires a burst of stale stops.
- **Status.** Each schedule reports `nextRunAt`, `lastRunAt`, `lastResult` (`success`, `error`, `skipped`), `lastError` and `runCount`.
- **Cleanup.** Deleting a machine deletes its schedules; a schedule whose machine has vanished is removed when it next fires.
- Every run is written to the audit log (`actor: system:scheduler`, `action: schedule.run`) and emits `io.kryton.schedule.executed` plus the usual machine event (`machine.started`, `machine.stopped`, `machine.restarted`, `snapshot.created`) with `data.trigger: "schedule"`.
- Limit: 100 schedules per project. Operator role on the project creates, edits and deletes; viewers can list.

Persisted in `KRYTON_SCHEDULES_FILE`.

---

## Machine TTL and auto-delete

`ttlMinutes` at create time (existing behaviour) schedules deletion. New for lab hygiene:

| Capability | How |
|------------|-----|
| Default TTL for every new machine | `KRYTON_DEFAULT_TTL_MINUTES` (Helm `defaultTTLMinutes`), applied when a create omits `ttlMinutes` |
| Extend, reset or cancel later | `krytonctl ttl <machine-id> 1440` (expire 24 h from now) or `krytonctl ttl <machine-id> 0` (never expire); `PUT /api/v1/machines/{id}/ttl` with `{"ttlMinutes": N}` |
| Early warning | `io.kryton.machine.expiring` is published once when a machine comes within `KRYTON_TTL_WARN` (default `15m`, `0` disables) of expiry, so a webhook can notify the owner |
| Expiry sweep | the reconciler deletes expired machines every `KRYTON_RECONCILE_INTERVAL` (default 30 s), emits `io.kryton.machine.expired` and audits it (`actor: system:ttl`, `action: machine.expire`) |

The override is shown in `expiresAt` on machine reads (`null` means never). With a default TTL configured, an operator can still make a machine permanent with `ttl … 0`; the default is a hygiene net, not an enforcement mechanism. Maximum is 43200 minutes (30 days).

---

## Project quotas

Cap what each project may hold. A limit of `0` means unlimited.

| Limit | Counts |
|-------|--------|
| `maxMachines` | machines in the project (excluding those being deleted) |
| `maxVCPU` | sum of `compute.cpu` |
| `maxMemoryMiB` | sum of `compute.memoryMiB` |
| `maxDiskGiB` | sum of the primary disk plus any extra dockur disks |

Stopped machines still count, because they keep their allocation.

### Configure

```bash
# Defaults for every project without an override
KRYTON_QUOTA_MAX_MACHINES=20  KRYTON_QUOTA_MAX_VCPU=64
KRYTON_QUOTA_MAX_MEMORY_MIB=262144  KRYTON_QUOTA_MAX_DISK_GIB=4000

# Per-project overrides at runtime (admin on the project); persisted in KRYTON_QUOTAS_FILE
krytonctl quota set lab --machines 5 --vcpu 16 --memory 65536 --disk 500
krytonctl quota list          # limits and current usage for every project you can view
krytonctl quota clear lab     # fall back to the default
```

### Enforcement

Checked on `POST /api/v1/machines`, immediately before the provider call. Creates in a project are serialized while a quota applies, so two simultaneous requests cannot both squeeze under the limit. Violations return `error.code: "QUOTA_EXCEEDED"` with the dimension, the limit, current usage and the requested amount in `message`:

| Status | When | What to do |
|--------|------|------------|
| `409 Conflict` | the project's current usage plus this machine would exceed a limit, but this machine alone would fit | delete or shrink other machines, or raise the quota |
| `403 Forbidden` | this single machine is larger than the project's limit and can never fit | request a smaller machine, or ask an admin to raise the quota |

```json
{"error":{"code":"QUOTA_EXCEEDED",
  "message":"project quota exceeded: vCPU limit 10, in use 8, requested 4",
  "hint":"Delete or resize other machines in project \"lab\", or ask an admin to raise the quota (PUT /api/v1/quotas/lab).",
  "requestId":"…"}}
```

Rejections are counted in `kryton_quota_rejections_total`, appear in the audit log with the reason, and usage versus limit is exported as `kryton_quota_usage` / `kryton_quota_limit`. Quotas apply at create only: lowering a limit never deletes existing machines.

---

## Environment reference

| Variable | Default | Purpose |
|----------|---------|---------|
| `KRYTON_METRICS_TOKEN` / `KRYTON_METRICS_TOKEN_FILE` | unset | Dedicated `/metrics` bearer token |
| `KRYTON_METRICS_PUBLIC` | `false` | Leave `/metrics` open with auth enabled |
| `KRYTON_AUDIT_FILE` | unset | JSONL audit sink |
| `KRYTON_AUDIT_MAX_ENTRIES` | `10000` | In-memory audit window |
| `KRYTON_AUDIT_MAX_FILE_BYTES` | `10485760` | Rotation size |
| `KRYTON_AUDIT_MAX_FILES` | `5` | Rotated files kept |
| `KRYTON_WEBHOOKS_FILE` | `~/.kryton/webhooks.json` | Subscriptions (delivery log beside it) |
| `KRYTON_WEBHOOK_ALLOW_HOSTS` | unset | CIDRs / IPs / hostnames exempt from the SSRF guard |
| `KRYTON_SCHEDULES_FILE` | `~/.kryton/schedules.json` | Schedules and TTL overrides |
| `KRYTON_DEFAULT_TTL_MINUTES` | `0` | TTL for creates that omit it |
| `KRYTON_TTL_WARN` | `15m` | Lead time for `machine.expiring` (`0` disables) |
| `KRYTON_QUOTAS_FILE` | `~/.kryton/quotas.json` | Per-project quota overrides |
| `KRYTON_QUOTA_MAX_MACHINES` `_VCPU` `_MEMORY_MIB` `_DISK_GIB` | `0` | Default quota |

## Limits at a glance

| Limit | Value |
|-------|-------|
| Audit page size | 500 |
| Audit memory window | 10 000 entries (configurable) |
| Webhook subscriptions | 50 per project, 20 event patterns each |
| Webhook attempts | 6 (1 s, 5 s, 30 s, 2 min, 10 min backoff) |
| Webhook delivery log | 500 deliveries |
| Schedules | 100 per project |
| Scheduler resolution / catch-up | 15 s / 5 min |
| Max TTL | 43 200 minutes |
