//go:build !hosted

package config

// HostedBuild is false in the self-hosted image. Build with -tags hosted
// for the multi-org service.
const HostedBuild = false
