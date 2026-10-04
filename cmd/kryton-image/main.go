// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

// Command kryton-image acquires verified images and exports deployment inputs.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/zyvorai/kryton/internal/catalog"
	"github.com/zyvorai/kryton/internal/imagebuild"
	"github.com/zyvorai/kryton/internal/libvirt"
	"github.com/zyvorai/kryton/internal/model"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: kryton-image list|validate|fetch|cdi|libvirt|cloud-init [flags]")
	}
	f := flag.NewFlagSet(args[0], flag.ContinueOnError)
	imagesFile := f.String("catalog", os.Getenv("KRYTON_IMAGES_FILE"), "optional catalog JSON")
	imageID := f.String("image", "ubuntu-24.04", "catalog image id")
	dir := f.String("dir", "/var/lib/kryton/images", "verified artifact directory")
	digest := f.String("sha256", "", "approved upstream SHA-256 digest (required for fetch)")
	source := f.String("url", "", "source override; cdi requires published verified artifact URL")
	ns := f.String("namespace", "kryton-images", "CDI namespace")
	sc := f.String("storage-class", "", "CDI StorageClass")
	name := f.String("name", "linux-template", "machine name")
	disk := f.String("disk", "/var/lib/kryton/template/root.qcow2", "libvirt root disk path")
	seed := f.String("seed", "/var/lib/kryton/template/seed.iso", "libvirt seed path")
	network := f.String("network", "default", "libvirt network name")
	if err := f.Parse(args[1:]); err != nil {
		return err
	}
	if f.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	cat, err := catalog.Load(*imagesFile)
	if err != nil {
		return err
	}
	emit := func(v any) error { e := json.NewEncoder(os.Stdout); e.SetIndent("", "  "); return e.Encode(v) }
	if args[0] == "list" {
		out := []model.Image{}
		for _, img := range cat.List() {
			if img.OS == "linux" {
				out = append(out, img)
			}
		}
		return emit(out)
	}
	if args[0] == "validate" {
		for _, img := range cat.List() {
			if img.OS != "linux" {
				continue
			}
			spec := templateSpec(img, *name)
			if _, err = model.CloudConfig(img, spec); err != nil {
				return fmt.Errorf("%s: %w", img.ID, err)
			}
			if _, err = libvirt.DomainXML(model.Machine{ID: "00000000-0000-4000-8000-000000000001", Spec: spec, ProviderRef: model.ProviderRef{Name: *name}}, img, *disk, *seed, *network); err != nil {
				return err
			}
		}
		return emit(map[string]string{"status": "templates valid", "bootValidation": "not performed"})
	}
	img, ok := cat.Get(*imageID)
	if !ok || img.OS != "linux" {
		return fmt.Errorf("unknown Linux image")
	}
	switch args[0] {
	case "fetch":
		a, err := (imagebuild.Store{Dir: *dir}).Fetch(context.Background(), img, *source, *digest)
		if err != nil {
			return err
		}
		return emit(a)
	case "cdi":
		items, err := imagebuild.CDI(img, *ns, *source, *sc)
		if err != nil {
			return err
		}
		return emit(map[string]any{"apiVersion": "v1", "kind": "List", "items": items})
	case "cloud-init":
		data, err := model.CloudConfig(img, templateSpec(img, *name))
		if err != nil {
			return err
		}
		fmt.Print(data)
		return nil
	case "libvirt":
		xml, err := libvirt.DomainXML(model.Machine{ID: "00000000-0000-4000-8000-000000000001", Spec: templateSpec(img, *name), ProviderRef: model.ProviderRef{Name: *name}}, img, *disk, *seed, *network)
		if err != nil {
			return err
		}
		fmt.Println(xml)
		return nil
	default:
		return fmt.Errorf("unknown image command %q", args[0])
	}
}
func templateSpec(img model.Image, name string) model.MachineSpec {
	return model.MachineSpec{Name: name, Image: img.ID, Compute: model.ComputeSpec{CPU: img.MinCPU, MemoryMiB: img.MinMemoryMiB}, Disk: model.DiskSpec{SizeGiB: img.DefaultDiskGB}}
}
