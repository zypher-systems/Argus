//go:build !hosted

package config

import "testing"

// The self-hosted build ignores ARGUS_HOSTED. Multi-org mode is the hosted build.
func TestCustomerBuildIsNotHosted(t *testing.T) {
	if HostedBuild {
		t.Fatal("HostedBuild must be false without -tags hosted")
	}
	t.Setenv("ARGUS_HOSTED", "true")
	t.Setenv("ARGUS_HOSTED_PLAN", "pro")
	if Load().Hosted {
		t.Fatal("ARGUS_HOSTED turned on hosted mode in the customer build")
	}
}
