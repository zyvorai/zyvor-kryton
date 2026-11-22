// Copyright 2026 Zyvor AI Labs · https://zyvor.dev
// SPDX-License-Identifier: LicenseRef-Zyvor-Production-2.0

package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadOperationsSettings(t *testing.T) {
	tokFile := filepath.Join(t.TempDir(), "metrics.token")
	if err := os.WriteFile(tokFile, []byte("scrape-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KRYTON_PROVIDER", "demo")
	t.Setenv("KRYTON_METRICS_TOKEN", "")
	t.Setenv("KRYTON_METRICS_TOKEN_FILE", tokFile)
	t.Setenv("KRYTON_AUDIT_FILE", "/var/log/kryton/audit.jsonl")
	t.Setenv("KRYTON_AUDIT_MAX_FILE_BYTES", "2048")
	t.Setenv("KRYTON_WEBHOOK_ALLOW_HOSTS", "10.0.0.0/8, hooks.corp ,10.0.0.0/8")
	t.Setenv("KRYTON_DEFAULT_TTL_MINUTES", "480")
	t.Setenv("KRYTON_TTL_WARN", "5m")
	t.Setenv("KRYTON_QUOTA_MAX_MACHINES", "7")
	t.Setenv("KRYTON_QUOTA_MAX_VCPU", "16")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MetricsToken != "scrape-secret" || cfg.AuditFile == "" || cfg.AuditMaxFileBytes != 2048 {
		t.Fatalf("metrics/audit: %+v", cfg)
	}
	if len(cfg.WebhookAllowHosts) != 2 || cfg.DefaultTTLMinutes != 480 || cfg.TTLWarn != 5*time.Minute {
		t.Fatalf("webhook/ttl: %+v", cfg)
	}
	if cfg.QuotaDefault.MaxMachines != 7 || cfg.QuotaDefault.MaxVCPU != 16 || cfg.QuotaDefault.MaxDiskGiB != 0 {
		t.Fatalf("quota: %+v", cfg.QuotaDefault)
	}
}

func TestValidateOperationsBounds(t *testing.T) {
	base := Config{Provider: "demo", Projects: []string{"default"}, DefaultProject: "default", AuthMode: "disabled"}
	for name, mut := range map[string]func(*Config){
		"ttl too large":  func(c *Config) { c.DefaultTTLMinutes = 99999 },
		"ttl negative":   func(c *Config) { c.DefaultTTLMinutes = -1 },
		"quota negative": func(c *Config) { c.QuotaDefault.MaxVCPU = -4 },
		"audit negative": func(c *Config) { c.AuditMaxFiles = -1 },
	} {
		c := base
		mut(&c)
		if err := c.Validate(); err == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
}
