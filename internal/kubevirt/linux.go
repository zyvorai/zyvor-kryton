// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package kubevirt

import (
	"fmt"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/model"
)

func resolveImage(cat *catalog.Catalog, imageID string) (model.Image, error) {
	if cat == nil {
		var err error
		cat, err = catalog.Load("")
		if err != nil {
			return model.Image{}, err
		}
	}
	img, ok := cat.Get(imageID)
	if !ok {
		return model.Image{}, fmt.Errorf("unknown image %q", imageID)
	}
	if !img.SupportsProvider("kubevirt") {
		return model.Image{}, fmt.Errorf("image does not support KubeVirt")
	}
	return img, nil
}
func applyImageProfile(vm map[string]any, img model.Image, spec model.MachineSpec) error {
	guest := vm["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
	domain := guest["domain"].(map[string]any)
	if img.Firmware == "bios" {
		domain["firmware"] = map[string]any{"bootloader": map[string]any{"bios": map[string]any{}}}
	}
	if img.OS != "linux" {
		return nil
	}
	domain["features"] = map[string]any{"acpi": map[string]any{}, "apic": map[string]any{}}
	devices := domain["devices"].(map[string]any)
	delete(devices, "tpm")
	data, err := model.CloudConfig(img, spec)
	if err != nil {
		return err
	}
	devices["disks"] = append(devices["disks"].([]any), map[string]any{"name": "cloudinit", "disk": map[string]any{"bus": "virtio"}})
	guest["volumes"] = append(guest["volumes"].([]any), map[string]any{"name": "cloudinit", "cloudInitNoCloud": map[string]any{"userData": data}})
	return nil
}
