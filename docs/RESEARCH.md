---
hero:
  eyebrow: Research
  title: Research and roadmap
---

# Research: machine-management APIs and what Kryton should borrow

Scope: the machine / VM control-plane field around Kryton (REST + CloudEvents over demo, dockur, libvirt and KubeVirt). Method: pages fetched on 2026-11-23 are cited with their URL. Anything not read from a fetched page is marked **[U]** (unverified, from prior knowledge) and should be re-checked before it drives a decision.

## What was read

| Source | What it showed |
|---|---|
| Kubernetes labels, https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/ | Selectors are comma-separated ANDs; equality (`=`, `==`, `!=`) and set-based (`in`, `notin`, bare key, `!key`). `!=` also matches objects lacking the key. Passed to list calls as `labelSelector`. |
| OpenStack Nova API ref, https://docs.openstack.org/api-ref/compute/ | Server metadata (255-byte keys and values), up to 50 server tags, list filters `tags`, `tags-any`, `not-tags`, `not-tags-any`; multi-create via `min_count`/`max_count` with the explicit warning that multi-create error handling is inconsistent and individual creates are preferred. |
| Stripe idempotent requests, https://docs.stripe.com/api/idempotent_requests | Client-chosen key up to 255 chars; first result saved and replayed; keys prunable after 24 h; params compared to the original and a mismatch is an error; validation failures and concurrent conflicts are not saved. |
| IETF draft-ietf-httpapi-idempotency-key-header-09, https://datatracker.ietf.org/doc/draft-ietf-httpapi-idempotency-key-header/ | `Idempotency-Key` header as a Structured Field string; payload fingerprint optional; 422 for key reuse with a different payload; 409 for a retry while the original is in flight; servers should validate the key format and scope lookups by client identity to stop guessing attacks. |
| Redfish ComputerSystem schema, https://redfish.dmtf.org/schemas/v1/ComputerSystem.v1_20_0.json | Resources advertise an `Actions` object (`#ComputerSystem.Reset`, and others): the resource says which actions exist. The `ResetType` allowable values were in the truncated part of the page and are **[U]**. |
| KubeVirt lifecycle, https://kubevirt.io/user-guide/user_workloads/lifecycle/ | start, stop (forced stop with `--grace-period 0 --force` risks data loss), pause/unpause. Restart, migration and run strategies are not on that page. |
| QEMU guest agent, https://www.qemu.org/docs/master/interop/qemu-ga.html | Host-to-guest daemon: filesystem freeze/thaw with hooks, file access, set password, guest info, set time, suspend; RPCs can be allow/block-listed. Shutdown and NIC info were not confirmed on the page. |
| CloudEvents, https://cloudevents.io/ | Landing page only: spec versions 1.0 to 1.0.2 and CloudEvents SQL v1 (approved 2024-06-13) as a standard filter language. Attribute details are **[U]**. |
| Proxmox API viewer, https://pve.proxmox.com/pve-docs/api-viewer/index.html | A JavaScript app; nothing usable was returned. Everything below about Proxmox is **[U]**. |

Not fetched, so everything about them is **[U]**: libvirt, Harvester, Cluster API, vSphere/PowerCLI, cloud-init/sysprep/unattend, Packer.

## What these systems do, and what Kryton lacks

1. **Selection by metadata.** Kubernetes labels, Nova tags/metadata, Proxmox tags **[U]**, vSphere tags **[U]**. Kryton accepts `spec.labels` on create (validated key/value patterns) but nothing can query them: `GET /machines` returns every machine, the OpenAPI create schema does not document `labels`, the CLI cannot set them, and the label map had no size cap (a 1 MiB body could carry thousands of labels).
2. **Safe retries of create.** Stripe and the IETF draft agree on one pattern. Kryton's create was not idempotent: a portal that times out and retries gets `409 CONFLICT` (name clash) or, with generated names, a duplicate machine that counts against quota.
3. **Fleet operations.** vSphere PowerCLI pipes `Get-VM | Start-VM` **[U]**, Proxmox has start-all/stop-all per node **[U]**, Nova has multi-create but warns about its error model. Kryton callers loop client-side with no per-item result contract and no selector.
4. **Event queries.** CloudEvents standardises the envelope and a filter language (CloudEvents SQL). Kryton's `GET /events` took only `limit`; consumers filtered client-side.
5. **Discoverable actions.** Redfish resources list their own allowed actions. Kryton's UI and CLI infer them from state and capabilities themselves.
6. **Out of scope or too large** (see ranked table): clone from snapshot, resize, live migration, guest-agent file/password RPCs, a Cluster API provider, sysprep/Packer pipelines.

## Ranked ideas

| Rank | Idea | Rationale | Sources | Decision |
|---|---|---|---|---|
| 1 | Label selectors on list (`?selector=env=ci,tier!=web,owner`), documented labels, `--label` in the CLI | Smallest change that unlocks fleets, bulk ops and portals; provider-agnostic (pure API layer); Kubernetes grammar is familiar. Also fixes the unbounded label map. | Kubernetes labels, Nova | Built |
| 2 | `Idempotency-Key` on `POST /machines` | Prevents duplicate machines on client retries; follows the IETF draft (422/409 semantics, fingerprint, per-caller scoping). | Stripe, IETF draft | Built |
| 3 | Bulk lifecycle: `POST /machines/actions` with ids or selector, per-item results | Replaces client loops; per-item outcome avoids Nova's multi-create ambiguity; capped batch size. | PowerCLI **[U]**, Nova | Built (start, stop only) |
| 4 | Event filters on `GET /events` (`type`, `machineId`, `since`) | Cheap, read-only; CloudEvents consumers expect to filter by type and subject. | CloudEvents | Built |
| 5 | `allowedActions` on a machine (Redfish style) | Removes duplicated UI logic; needs a per-provider capability matrix to be honest. | Redfish | Deferred |
| 6 | Clone machine from snapshot | Needs provider-level support in four backends (CDI clone, qcow2 copy, compose copy) and cannot be tested without Docker, libvirt or Kubernetes. | Nova, Proxmox **[U]** | Rejected for now |
| 7 | CloudEvents SQL subscriptions | Large grammar and evaluator; the selector and filters above cover most uses. | cloudevents.io | Deferred |
| 8 | Guest-agent RPCs (freeze/thaw, set password) | libvirt and KubeVirt only, needs a live guest; the Kryton job agent already covers dockur. | QEMU GA | Rejected for now |
| 9 | Cluster API provider | A separate project (controller and CRDs), against the "one REST contract" boundary. **[U]** | CAPI **[U]** | Rejected |

## Boundaries checked

From CONTRIBUTING.md, README and docs: provider-specific behaviour stays behind `internal/provider.Provider` (all four features are API-layer only); raw provider identifiers are not the public machine ID (selectors and bulk results use Kryton UUIDs); new Go files carry the licence header; docs, README counts and the `[Unreleased]` changelog are updated; the three copies of `openapi.yaml` stay identical; the generated dockur password is "never stored or returned again", so an idempotent replay returns the machine without `generatedPassword`; the project stays at two Go dependencies; no Microsoft media or activation features are touched.
