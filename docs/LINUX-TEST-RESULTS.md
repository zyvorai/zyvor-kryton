# Linux template validation — 4 October 2026

Base commit: `3aabfb285fbcf215dd52b79ca34dfeadb56f0b46`.
Runtime: Linux amd64, Go 1.26.6. Guest hardware and a cluster are unavailable.

| Check | Result |
|---|---|
| `make check` (format, full Go suite, vet, all three CLI/daemon builds) | Passed |
| `go test -race ./...` | Passed |
| golangci-lint 2.14.0 | Passed, zero issues |
| govulncheck | Passed, no vulnerabilities found with Go 1.26.6 |
| KubeVirt REST VM creation for six Linux and twelve Windows templates | Passed with local Kubernetes REST fixture |
| CDI persistent import/DataSource exports for all six Linux templates | Passed |
| Native libvirt lifecycle, restart recovery, project isolation, failure retention | Passed with injected virsh runner |
| Libvirt domain XML schema for all six Linux templates | Passed using installed libvirt schema and xmllint |
| Real QCOW2 conversion/resize and NoCloud ISO extraction | Passed; uses qemu-img, genisoimage and isoinfo |
| All six cloud-config documents | Passed upstream cloud-init 26.1 JSON Schema validation |
| All six distribution source URLs | HTTP HEAD returned 200; no OS image boot/download claimed |
| JS/shell syntax, license headers, diff whitespace | Passed |
| Gosec 2.29.0 | Not clean: 40 existing findings; baseline has 41; no new file/rule/detail findings after documented review of controlled file/command operations |
| Real guest boot / SSH / cloud-init / root expansion on libvirt | Ubuntu 24.04 and Debian 12 passed (see below); other four images not fetched |
| Real guest boot on KubeVirt | Not executed; requires a live cluster |

The gosec repository-wide CI gate can remain red because of the existing
findings. They were not globally suppressed. New artifact/libvirt operations
include narrowly documented annotations for operator-controlled paths, a fixed
host-tool allowlist, and intentional QEMU-group/public-artifact permissions.
These annotations are not evidence of guest boot certification.

## Native libvirt on a real KVM host

Host: Ubuntu 24.04.5, kernel 6.8.0, libvirt 10.0.0, QEMU 8.2.2, `qemu:///system`,
network `default`. Deployed with
`./scripts/deploy-remote.sh <host> <user> --key --apikey --provider libvirt --quick`.

Images were acquired with `kryton-image fetch`:

| Image | Digest source | SHA-256 |
|---|---|---|
| ubuntu-24.04 | `noble/current/SHA256SUMS`, `gpgv` good signature from the Ubuntu cloud image key | `6a81c375…5bb4d2` |
| debian-12 | `bookworm/latest/SHA512SUMS` over HTTPS (no signature published there); bytes matched SHA-512, SHA-256 derived | `7b3faf64…bdecad` |

`KRYTON_E2E_IMAGES="ubuntu-24.04 debian-12" scripts/e2e-linux-templates.sh`,
run on the host so guests are reachable on the libvirt network:

```
ubuntu-24.04: boot, SSH, cloud-init, guest agent, disk expansion passed
debian-12: boot, SSH, cloud-init, guest agent, disk expansion passed
```

Both images finish in about 2½ minutes together (2m29s on the final build). The first real runs found three
defects, now fixed with regression tests:

- **Debian never configured.** Debian's `cloud-amd64` kernel has no AHCI
  driver, so the SATA NoCloud CD-ROM was invisible (hostname stayed
  `localhost`, no DHCP). The seed now sits on a virtio-scsi controller.
- **SSH targeted loopback.** The guest agent lists `lo` first, so
  `ipAddresses[0]` was `127.0.0.1`. Loopback and link-local addresses are now
  dropped and IPv4 is listed first.
- **QEMU could not open disks.** Per-machine directories are `0750`; the
  machine data dir must be setgid and owned by the QEMU group.
  `deploy-remote.sh --provider libvirt` now creates it `2750 root:kvm`.

Listing images re-hashed every artifact on each request (about 1 s for two
images). Listings now check the manifest and size; provisioning still verifies
the full digest.

## Operator console

The UI was rebuilt as React + Vite (`web/`, built into `cmd/krytond/web`).
Checked in headless Chrome against the libvirt host at 1440 px and 390 px, in
light and dark themes: sign in (rejected and accepted keys), overview, images
(two stored, four "Fetch required" with the exact `kryton-image` command),
create Ubuntu 24.04 with an SSH key, drawer showing the agent IP and the
`ssh ubuntu@<ip>` command (used successfully from the host), delete with
confirmation. No console errors. `npm run typecheck` and 28 vitest tests pass.

## Remaining

Run `scripts/e2e-linux-templates.sh` against KubeVirt and for the remaining
four images on libvirt before promoting the templates.
Native libvirt snapshots, VNC, live migration, Windows and ARM64 support,
ISO installation, offline image customization and artifact signing are future
extensions. This change provides cloud-image acquisition, Linux first-boot
initialization, deployment templates and native machine lifecycle.
