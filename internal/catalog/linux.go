// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package catalog

import "github.com/zyvorai/kryton/internal/model"

// Source URLs are discovery defaults, NOT immutable releases. Acquisition requires
// an operator-approved SHA-256; the resulting artifact is addressed by its digest.
func linuxDefaults() []model.Image {
	defs := []struct{ id, name, version, family, user, source string }{
		{"ubuntu-26.04", "Ubuntu 26.04 LTS", "26.04", "ubuntu", "ubuntu", "https://cloud-images.ubuntu.com/resolute/current/resolute-server-cloudimg-amd64.img"},
		{"ubuntu-24.04", "Ubuntu 24.04 LTS", "24.04", "ubuntu", "ubuntu", "https://cloud-images.ubuntu.com/noble/current/noble-server-cloudimg-amd64.img"},
		{"ubuntu-22.04", "Ubuntu 22.04 LTS", "22.04", "ubuntu", "ubuntu", "https://cloud-images.ubuntu.com/jammy/current/jammy-server-cloudimg-amd64.img"},
		{"debian-13", "Debian 13", "13", "debian", "debian", "https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2"},
		{"debian-12", "Debian 12", "12", "debian", "debian", "https://cloud.debian.org/images/cloud/bookworm/latest/debian-12-genericcloud-amd64.qcow2"},
		// EL10 builds target x86-64-v3; the guest CPU model must expose AVX2.
		{"rocky-10", "Rocky Linux 10", "10", "rocky", "rocky", "https://download.rockylinux.org/pub/rocky/10/images/x86_64/Rocky-10-GenericCloud-Base.latest.x86_64.qcow2"},
		{"rocky-9", "Rocky Linux 9", "9", "rocky", "rocky", "https://download.rockylinux.org/pub/rocky/9/images/x86_64/Rocky-9-GenericCloud-Base.latest.x86_64.qcow2"},
		{"almalinux-10", "AlmaLinux 10", "10", "almalinux", "almalinux", "https://repo.almalinux.org/almalinux/10/cloud/x86_64/images/AlmaLinux-10-GenericCloud-latest.x86_64.qcow2"},
		{"almalinux-9", "AlmaLinux 9", "9", "almalinux", "almalinux", "https://repo.almalinux.org/almalinux/9/cloud/x86_64/images/AlmaLinux-9-GenericCloud-latest.x86_64.qcow2"},
		{"centos-stream-10", "CentOS Stream 10", "10", "centos", "cloud-user", "https://cloud.centos.org/centos/10-stream/x86_64/images/CentOS-Stream-GenericCloud-10-latest.x86_64.qcow2"},
		// Fedora publishes no "latest" alias; bump the compose with each release.
		{"fedora-44", "Fedora 44", "44", "fedora", "fedora", "https://download.fedoraproject.org/pub/fedora/linux/releases/44/Cloud/x86_64/images/Fedora-Cloud-Base-Generic-44-1.7.x86_64.qcow2"},
	}
	out := make([]model.Image, 0, len(defs))
	for _, d := range defs {
		out = append(out, model.Image{ID: d.id, Name: d.name, Version: d.version, Family: d.family, OS: "linux", Architecture: "amd64", Firmware: "bios", DefaultUser: d.user, SourceURL: d.source, Providers: []string{"libvirt", "kubevirt"}, Description: "Cloud-init enabled Linux image for libvirt and KubeVirt; acquire and validate before production.", MinCPU: 1, MinMemoryMiB: 1024, DefaultDiskGB: 20, Tags: []string{"linux", "cloud-init"}})
	}
	return out
}
