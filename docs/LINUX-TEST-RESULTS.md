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
| Real guest boot / SSH / cloud-init / root expansion on libvirt | All six images passed (see below) |
| Real guest boot on KubeVirt | Blocked by lab cluster networking: guest boots, but DNS fails (see below) |

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
| ubuntu-22.04 | `jammy/current/SHA256SUMS`, `gpgv` good signature from the Ubuntu cloud image key | `0c9811a8…57b210e` |
| debian-13 | `trixie/latest/SHA512SUMS` over HTTPS; bytes matched SHA-512, SHA-256 derived | `b2aca2be…f23b9d1` |
| rocky-9 | `CHECKSUM` signed by the Rocky release key 2022 (`21CB256A…350D275D`), good signature | `92c206cc…ab4eec8` |
| almalinux-9 | `CHECKSUM` signed by the AlmaLinux OS 9 key (`BF18AC28…CB86B3716`), good signature | `6bdab637…002d74b` |

`scripts/e2e-linux-templates.sh` with all six images, run on the host so
guests are reachable on the libvirt network (about 11 minutes):

```
debian-13: boot, SSH, cloud-init, guest agent, disk expansion passed
rocky-9: boot, SSH, cloud-init, guest agent, disk expansion passed
almalinux-9: boot, SSH, cloud-init, guest agent, disk expansion passed
ubuntu-22.04: boot, SSH, cloud-init, guest agent, disk expansion passed
ubuntu-24.04: boot, SSH, cloud-init, guest agent, disk expansion passed
debian-12: boot, SSH, cloud-init, guest agent, disk expansion passed
```

The first real runs found these defects, now fixed with regression tests:

- **Debian 13 boot loop.** Domains had no video adapter. Debian 13's BIOS
  GRUB resets the guest when it hands off to the kernel on such a machine
  (reproduced with plain QEMU `-nodefaults -vga none`). Domains now include a
  VGA device.
- **Rocky and AlmaLinux version check.** They report `VERSION_ID="9.6"` for
  the `9` template; the gate now accepts point releases.

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

## KubeVirt on the lab cluster

Cluster: k3s with Cilium (kube-proxy replacement, socket load balancing),
KubeVirt v1.4.0, CDI and Longhorn. Ubuntu 24.04 and Debian 12 CDI
DataSources were imported from digest-checked URLs; krytond ran with
`KRYTON_PROVIDER=kubevirt` and `KRYTON_STORAGE_CLASS=longhorn`.

Ubuntu 24.04 provisioned, booted to a login prompt and applied its NoCloud
hostname, and SSH with the test key worked on the pod IP. The gate still
cannot pass on this cluster: the guest resolves through the cluster DNS
ClusterIP via masquerade, and Cilium's socket load balancer does not translate
packets forwarded from the VM's tap device. CoreDNS pod IPs and public DNS are
reachable from the guest, but the ClusterIP is not, so cloud-init hangs on
`apt-get update` and `qemu-guest-agent` is never installed. Fixing this is a
cluster change (for example Cilium `bpf-lb-sock-hostns-only: true`), not a
Kryton change.

The run also fixed two gate defects for KubeVirt:

- A long-lived `virtctl port-forward` stops accepting connections after its
  first failed dial, which happens while the guest is still booting. Each SSH
  attempt now opens its own tunnel with `virtctl port-forward --stdio`.
- Filesystem-mode PVCs lose a few percent to filesystem overhead (a 20 GiB
  Longhorn request gave a 19.5 GiB disk), so the KubeVirt disk check allows 5%.

## Remaining

Re-run `scripts/e2e-linux-templates.sh` against KubeVirt on a cluster where
VM masquerade traffic can reach cluster DNS before promoting the KubeVirt
templates.
Native libvirt snapshots, VNC, live migration, Windows and ARM64 support,
ISO installation, offline image customization and artifact signing are future
extensions. This change provides cloud-image acquisition, Linux first-boot
initialization, deployment templates and native machine lifecycle.
