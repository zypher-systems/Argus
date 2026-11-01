# Changelog

All notable changes to Argus are recorded here.

## [2.16.1] — 2026-11-01

Fixes for results that said more than Argus knew, and for what an import left behind.

### Fixed

- **Private incidents stay private.** An incident marked private is no longer sent to status page subscribers, by email or webhook.
- **A broken response is a failed check.** HTTP and transaction checks now fail when a response breaks off or stalls. Before, they could report up, even with a keyword rule.
- **Status pages stop vouching for monitors nothing is checking.** A monitor overdue for a check, by twice its interval plus 15 seconds, shows as Unknown, and the page and its badge say unknown instead of operational. A monitor that was down when its checks stopped keeps showing the outage.
- **Importing an Argus export restores its notification channels** and puts them back on their monitors. A channel the install already has, with the same name, type and destination, is reused.
- **24-hour uptime holds through live updates.** The console no longer replaces it with a figure worked out from the last 80 checks.

### Changed

- **An HTTP check on a slow stream now fails at the timeout.** A response that never finishes and sends less than 1 MiB used to pass. Such endpoints need a `HEAD` request.
- **A status page with a monitor not checked yet says unknown,** including a scheduled heartbeat waiting for its first ping.

### Added

- **CI on GitHub.** Pull requests into `dev` and `main` run lint, tests, the frontend build, and the install smoke against the real images.

## [2.16.0] — 2026-10-21

First public release, under the MIT license.

Argus is self-hosted uptime monitoring. Monitors, alerts, status pages, transactions, teams and single sign-on, probe nodes, SLA reports, and the audit log are included.

Images: `ghcr.io/zypher-systems/argus/backend` and `ghcr.io/zypher-systems/argus/web`, for `linux/amd64` and `linux/arm64`.
