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

## Round 2 (2026-11-24)

Searches run this round (web search summaries, not full page reads, so detail beyond what is stated is **[U]**):

| Source | What it showed | Used for |
|---|---|---|
| Proxmox VE API and `pve-bulk` (pve.proxmox.com `qm(1)`, github.com/ponceto/pve-bulk) | `qm clone --snapname` clones from a snapshot; tags are plain metadata; there is no native bulk endpoint, so bulk snapshot/rollback/delete lives in community scripts; clones lock the guest. | Bulk `snapshot` and `delete` as a built-in, with per-machine results. Clone-from-snapshot stays rejected (provider work). |
| Redfish ResetType (DMTF DSP2046, vendor examples) | Services advertise allowable actions per resource (`ResetType@Redfish.AllowableValues`, `ResetActionInfo`); values differ by implementation, so clients must read them from the service. | `allowedActions` on machines, computed from state and provider support (rank 5 from round 1, now built). |
| AWS EC2 `RebootInstances` / `DryRun` (docs.aws.amazon.com) | Actions take a list of ids; `DryRun` checks permission and state without acting; targets are chosen with a separate filtered describe call. | `dryRun` on bulk actions, plus `state`/`name` list filters to choose targets. |
| Google Compute Engine labels (cloud.google.com `instances.setLabels`) | Labels are mutated with a `labelFingerprint` for optimistic concurrency (`412` on a stale value). | Considered for label editing; **deferred**: labels live in the providers, so mutating them needs a change to all four, which can't be tested here without Docker, libvirt or Kubernetes. |

Decisions: built bulk snapshot/delete with `dryRun` and a delete `confirm` guard (the round 1 note "bulk delete is intentionally not offered" is superseded by the guard), `allowedActions`, `state`/`name` filters, `groupBy` summaries and SSE event filters. All are API-layer only. Bulk restart stays out: only guest-capable providers can restart.

## Round 3 (2026-11-25): VM fleet consoles

Web searches this round; the pages marked "fetched" were also read directly (through a summarising fetcher, so quotes are short). Anything else is a search summary and is **[U]** beyond what is stated.

| Source | Read | What it showed | Used for |
|---|---|---|---|
| Proxmox `qm.conf`, https://pve.proxmox.com/wiki/Manual:_qm.conf | fetched (first part) | `protection`: "This will disable the remove VM and remove disk operations." `tags` are "only meta information"; `startup` gives ordered start/stop with delays. | Delete protection. Ordered startup deferred. |
| Proxmox GUI chapter, https://pve.proxmox.com/pve-docs/chapter-pve-gui.html | fetched | Tag view, Bulk Start / Shutdown / Migrate on a node, task log, cluster search. | Bulk actions and filters in the web console. |
| Incus instance options, https://linuxcontainers.org/incus/docs/main/reference/instance_options/ | fetched | `security.protection.delete` "Prevents the instance from being deleted"; `snapshots.schedule`, `snapshots.expiry`, `boot.autostart.priority`. | Delete protection; retention. |
| Harvester backup and snapshot schedules, https://docs.harvesterhci.io/v1.8/vm/backup-restore | fetched | A schedule keeps a "retain" count, deleting the oldest beyond it, and is suspended after "Max Failure" consecutive failures; resume is manual. | `retain`, `maxFailures`. |
| OKD VM delete protection, https://docs.okd.io/4.20/virt/managing_vms/virt-enabling-disabling-vm-delete-protection.html | search summary (the fetched page did not include the body) | Label `kubevirt.io/vm-delete-protection`; the console disables Delete while set. **[U]** | Disabled Delete button in the console. |
| KubeVirt snapshot/restore guide, https://kubevirt.io/user-guide/storage/snapshot_restore_api/ | search summary | `failureDeadline` on snapshots; no retention or schedule feature was found. **[U]** | Retention lives in Kryton, and counts only ready snapshots because creation is asynchronous. |

Decisions:

- **Delete protection** (built) is held by the API layer, so all four providers get it without provider changes. The cost: it does not stop `kubectl delete`, and on the Helm chart's `emptyDir` it does not survive pod replacement. Storing it as a VM label (the KubeVirt way) would need provider work that cannot be tested here without a cluster.
- **Retention** (built) only deletes snapshots the schedule itself recorded, never by name pattern, and waits for N ready ones. Age-based expiry (Incus) was not built: count-based retention cannot delete the last good copy when the schedule stops running.
- **Run now** and the **snapshot inventory** (built) support the two above; neither is copied from a fetched page.
- **Ordered or delayed startup** (Proxmox `startup`, Incus `boot.autostart.priority`): deferred; it needs a long-running operation resource rather than one synchronous request.
- **Live migration, clone, resize**: still rejected for the reasons in round 1.
