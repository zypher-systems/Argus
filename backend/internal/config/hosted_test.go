package config

import "testing"

func TestHostedModeNeedsTheHostedBuild(t *testing.T) {
	t.Setenv("ARGUS_HOSTED", "true")
	t.Setenv("ARGUS_HOSTED_PLAN", "pro")
	cfg := Load()
	if cfg.Hosted != HostedBuild {
		t.Fatalf("hosted=%v in a build with HostedBuild=%v", cfg.Hosted, HostedBuild)
	}
}
