// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package libvirt

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"github.com/zyvorai/kryton/internal/model"
)

func escape(s string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

// DomainXML uses managed, absolute disk paths. Linux defaults to BIOS; firmware
// auto-selection is delegated to libvirt when the catalog explicitly requests EFI.
func DomainXML(m model.Machine, img model.Image, disk, seed, network string) (string, error) {
	if err := model.ValidateMachineSpec(m.Spec); err != nil {
		return "", err
	}
	if img.OS != "linux" || img.Architecture != "amd64" {
		return "", fmt.Errorf("libvirt currently supports amd64 Linux cloud images")
	}
	firmware := ""
	if img.Firmware == "efi" {
		firmware = " firmware='efi'"
	} else if img.Firmware != "bios" {
		return "", fmt.Errorf("invalid firmware")
	}
	return fmt.Sprintf(`<domain type='kvm'><name>%s</name><uuid>%s</uuid><memory unit='MiB'>%d</memory><vcpu>%d</vcpu><os%s><type arch='x86_64' machine='q35'>hvm</type><boot dev='hd'/></os><features><acpi/><apic/></features><cpu mode='host-model'/><devices><disk type='file' device='disk'><driver name='qemu' type='qcow2'/><source file='%s'/><target dev='vda' bus='virtio'/></disk><controller type='scsi' index='0' model='virtio-scsi'/><disk type='file' device='cdrom'><driver name='qemu' type='raw'/><source file='%s'/><target dev='sda' bus='scsi'/><readonly/></disk><interface type='network'><source network='%s'/><model type='virtio'/></interface><channel type='unix'><target type='virtio' name='org.qemu.guest_agent.0'/></channel><serial type='pty'><target port='0'/></serial><console type='pty'><target type='serial' port='0'/></console></devices></domain>`, escape(m.ProviderRef.Name), escape(m.ID), m.Spec.Compute.MemoryMiB, m.Spec.Compute.CPU, firmware, escape(disk), escape(seed), escape(network)), nil
}
