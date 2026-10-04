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
| Real guest boot / SSH / cloud-init / root expansion on both targets | Not executed; requires `/dev/kvm` and a live cluster |

The gosec repository-wide CI gate can remain red because of the existing
findings. They were not globally suppressed. New artifact/libvirt operations
include narrowly documented annotations for operator-controlled paths, a fixed
host-tool allowlist, and intentional QEMU-group/public-artifact permissions.
These annotations are not evidence of guest boot certification.

The real acceptance script is `scripts/e2e-linux-templates.sh`; run it once
against libvirt and once against KubeVirt before promoting the templates.
Native libvirt snapshots, VNC, live migration, Windows and ARM64 support,
ISO installation, offline image customization and artifact signing are future
extensions. This change provides cloud-image acquisition, Linux first-boot
initialization, deployment templates and native machine lifecycle.
