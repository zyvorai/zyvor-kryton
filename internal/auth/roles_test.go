// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func writeKeys(t *testing.T, body string) string {
	t.Helper()
	path := t.TempDir() + "/keys.json"
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func bearer(token string) *http.Request {
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

func TestLegacyKeyWithoutRoleIsAdmin(t *testing.T) {
	path := writeKeys(t, `{"keys":[{"name":"old","sha256":"`+HashToken("t1")+`","projects":["*"]},{"name":"explicit","sha256":"`+HashToken("t2")+`","role":"viewer","projects":["lab"]}]}`)
	a, err := New(Config{Mode: "apikey", APIKeysFile: path})
	if err != nil {
		t.Fatal(err)
	}
	p, err := a.authenticate(bearer("t1"))
	if err != nil || p.Role != Admin || !Can(p, "anything", Admin) {
		t.Fatalf("legacy key should be admin: %+v %v", p, err)
	}
	p, _ = a.authenticate(bearer("t2"))
	if !Can(p, "lab", Viewer) || Can(p, "lab", Operator) {
		t.Fatalf("explicit viewer key wrong: %+v", p)
	}
}

func TestPerProjectGrants(t *testing.T) {
	path := writeKeys(t, `{"keys":[{"name":"mixed","sha256":"`+HashToken("t")+`","role":"viewer","projects":["*"],"grants":{"lab":"operator","ops":"admin"}},{"name":"onlygrants","sha256":"`+HashToken("g")+`","grants":{"lab":"operator"}}]}`)
	a, err := New(Config{Mode: "apikey", APIKeysFile: path})
	if err != nil {
		t.Fatal(err)
	}
	p, _ := a.authenticate(bearer("t"))
	if !Can(p, "prod", Viewer) || Can(p, "prod", Operator) {
		t.Fatalf("prod should be viewer only: %+v", p)
	}
	if !Can(p, "lab", Operator) || Can(p, "lab", Admin) {
		t.Fatal("lab should be operator")
	}
	if !Can(p, "ops", Admin) {
		t.Fatal("ops should be admin")
	}
	g, _ := a.authenticate(bearer("g"))
	if !Can(g, "lab", Operator) || Can(g, "prod", Viewer) {
		t.Fatalf("grants-only key wrong: %+v", g)
	}
}

func TestInvalidKeyEntries(t *testing.T) {
	for name, body := range map[string]string{
		"bad role":  `{"keys":[{"name":"x","sha256":"` + HashToken("a") + `","role":"root","projects":["*"]}]}`,
		"bad grant": `{"keys":[{"name":"x","sha256":"` + HashToken("a") + `","grants":{"lab":"god"}}]}`,
		"no scope":  `{"keys":[{"name":"x","sha256":"` + HashToken("a") + `","role":"admin"}]}`,
	} {
		if _, err := New(Config{Mode: "apikey", APIKeysFile: writeKeys(t, body)}); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestProxyGrantsHeader(t *testing.T) {
	path := t.TempDir() + "/s"
	_ = os.WriteFile(path, []byte("s"), 0600)
	a, _ := New(Config{Mode: "proxy", TrustProxy: true, ProxySecretFile: path})
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("X-Kryton-Proxy-Secret", "s")
	r.Header.Set("X-Kryton-User", "bob")
	r.Header.Set("X-Kryton-Grants", "lab=operator, prod=viewer")
	p, err := a.authenticate(r)
	if err != nil || !Can(p, "lab", Operator) || Can(p, "prod", Operator) {
		t.Fatalf("proxy grants: %+v %v", p, err)
	}
	r.Header.Set("X-Kryton-Grants", "lab=bogus")
	if _, err := a.authenticate(r); err == nil {
		t.Fatal("bad grants accepted")
	}
}
