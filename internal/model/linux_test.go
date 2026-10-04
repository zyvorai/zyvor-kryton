// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCloudConfigEscapesStringsAndLocksPasswords(t *testing.T) {
	img := Image{OS: "linux", DefaultUser: "ubuntu"}
	spec := MachineSpec{Name: "guest", Initialization: &Initialization{SSHAuthorizedKeys: []string{`ssh-ed25519 AAAA comment: "quoted"`}}}
	data, err := CloudConfig(img, spec)
	if err != nil {
		t.Fatal(err)
	}
	var cfg map[string]any
	if err = json.Unmarshal([]byte(strings.TrimPrefix(data, "#cloud-config\n")), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg["ssh_pwauth"] != false || cfg["disable_root"] != true {
		t.Fatal("insecure config")
	}
	user := cfg["users"].([]any)[0].(map[string]any)
	if user["lock_passwd"] != true || user["name"] != "ubuntu" {
		t.Fatal("default user not password-locked")
	}
	if user["ssh_authorized_keys"].([]any)[0] != spec.Initialization.SSHAuthorizedKeys[0] {
		t.Fatal("key escaped incorrectly")
	}
}
func TestInvalidInitialization(t *testing.T) {
	for _, i := range []Initialization{{Username: "root;echo"}, {Username: "\nx"}, {SSHAuthorizedKeys: []string{"ssh-ed25519 bad!"}}, {SSHAuthorizedKeys: []string{"ssh-ed25519 AAAA\nusers:"}}, {SSHAuthorizedKeys: []string{"PRIVATE KEY"}}} {
		if i.Validate() == nil {
			t.Fatalf("accepted invalid init %+v", i)
		}
	}
}
