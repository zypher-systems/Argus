package version

// Version is the product identity shown to operators and in GET /api/instance.
// Override at build time with -ldflags "-X argus/internal/version.Version=...".
var Version = "2.16.0"

// SupportEmail is where a message that says "contact Zypher Systems" sends
// people. Naming nobody left them with nowhere to go.
const SupportEmail = "support@zyphersystems.com"
