package api

import (
	"testing"

	"argus/internal/store"
)

func TestRedactMonitorConfig(t *testing.T) {
	cfg := store.MonitorConfig{Auth: "bearer", AuthToken: "secret", AuthPassword: "pw"}
	redactMonitorConfig(&cfg)
	if cfg.AuthToken != "" || cfg.AuthPassword != "" {
		t.Fatalf("secrets leaked: %+v", cfg)
	}
}

func TestMergeAuthSecrets(t *testing.T) {
	existing := store.MonitorConfig{Auth: "bearer", AuthToken: "kept"}
	dst := store.MonitorConfig{Auth: "bearer", AuthToken: ""}
	mergeAuthSecrets(&dst, existing)
	if dst.AuthToken != "kept" {
		t.Fatalf("got %q", dst.AuthToken)
	}
	dst = store.MonitorConfig{Auth: "bearer", AuthToken: "new"}
	mergeAuthSecrets(&dst, existing)
	if dst.AuthToken != "new" {
		t.Fatalf("overwrite = %q", dst.AuthToken)
	}
}
