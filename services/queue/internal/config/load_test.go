package config

import "testing"

func TestLoadDefaultsAndOverrides(t *testing.T) {
	t.Setenv("SERVICE_PORT", "9999")
	t.Setenv("LOG_LEVEL", "debug")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.ServiceName != "queue" || cfg.Port != 9999 || cfg.LogLevel.String() != "DEBUG" {
		t.Fatalf("cfg = %+v", cfg)
	}
	if len(cfg.Dependencies) != 3 {
		t.Fatalf("dependencies = %d, want 3", len(cfg.Dependencies))
	}
}

func TestLoadRejectsBadPort(t *testing.T) {
	t.Setenv("SERVICE_PORT", "abc")

	if _, err := Load(); err == nil {
		t.Fatal("Load() must fail on invalid SERVICE_PORT")
	}
}
