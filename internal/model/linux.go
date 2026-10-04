// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Initialization configures a password-locked Linux guest using public SSH keys.
// JSON is a YAML subset, avoiding interpolation of user strings into cloud-config.
type Initialization struct {
	Username          string   `json:"username,omitempty"`
	SSHAuthorizedKeys []string `json:"sshAuthorizedKeys,omitempty"`
}

var linuxUsername = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

func (i Initialization) Validate() error {
	if i.Username != "" && !linuxUsername.MatchString(i.Username) {
		return fmt.Errorf("invalid Linux username")
	}
	if len(i.SSHAuthorizedKeys) > 32 {
		return fmt.Errorf("at most 32 SSH keys are allowed")
	}
	for _, k := range i.SSHAuthorizedKeys {
		if len(k) > 16384 || strings.ContainsAny(k, "\r\n") {
			return fmt.Errorf("invalid SSH public key")
		}
		fields := strings.Fields(k)
		if len(fields) < 2 || (fields[0] != "ssh-ed25519" && fields[0] != "ssh-rsa" && !strings.HasPrefix(fields[0], "ecdsa-sha2-")) {
			return fmt.Errorf("unsupported SSH public key type")
		}
		if _, err := base64.StdEncoding.DecodeString(fields[1]); err != nil {
			return fmt.Errorf("invalid SSH public key encoding")
		}
	}
	return nil
}
func (i Image) SupportsProvider(p string) bool {
	if p == "demo" {
		return true
	}
	if len(i.Providers) == 0 {
		return p == "kubevirt" || (p == "dockur" && i.DockurVersion != "")
	}
	for _, v := range i.Providers {
		if v == p {
			return true
		}
	}
	return false
}
func CloudConfig(img Image, spec MachineSpec) (string, error) {
	if img.OS != "linux" {
		return "", fmt.Errorf("cloud-init requires Linux")
	}
	i := Initialization{}
	if spec.Initialization != nil {
		i = *spec.Initialization
	}
	if err := i.Validate(); err != nil {
		return "", err
	}
	user := i.Username
	if user == "" {
		user = img.DefaultUser
	}
	if !linuxUsername.MatchString(user) {
		return "", fmt.Errorf("image has no valid default user")
	}
	userConfig := map[string]any{"name": user, "lock_passwd": true, "shell": "/bin/bash", "sudo": "ALL=(ALL) NOPASSWD:ALL"}
	if len(i.SSHAuthorizedKeys) > 0 {
		userConfig["ssh_authorized_keys"] = i.SSHAuthorizedKeys
	}
	config := map[string]any{"hostname": spec.Name, "manage_etc_hosts": true, "ssh_pwauth": false, "disable_root": true, "users": []any{userConfig}, "growpart": map[string]any{"mode": "auto", "devices": []string{"/"}, "ignore_growroot_disabled": false}, "resize_rootfs": true, "packages": []string{"qemu-guest-agent"}, "runcmd": [][]string{{"systemctl", "enable", "--now", "qemu-guest-agent"}}}
	b, err := json.Marshal(config)
	return "#cloud-config\n" + string(b) + "\n", err
}
