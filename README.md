# Argus

Self-hosted uptime monitoring — ping your infrastructure, get alerts, and watch it on a fast dashboard. Public status pages show names only; probe targets stay private.

It checks websites, APIs, hosts, DNS, certificates, domains, mail servers and
databases, alerts through the channels a team already uses, and publishes
status pages. What changed in each version is in [CHANGELOG.md](CHANGELOG.md).

![stack](https://img.shields.io/badge/stack-Go%2BReact%2BPostgres%2BDocker-6366f1)
![version](https://img.shields.io/badge/version-2.16.0-1f5bd8)

## Features

- ICMP ping, HTTP/HTTPS (GET/HEAD/POST, JSON path, keyword invert, proxy), TCP, TLS certificate, push/heartbeat, and groups
- Transaction monitors: up to 10 HTTP steps sharing cookies, values extracted from one response and used in the next, encrypted secrets, and alerts that name the failing step
- DNS records: A, AAAA, CNAME, MX, NS and TXT, with an expected answer
- Domain registration expiry over RDAP, warned well before it lapses
- Mail servers: SMTP, IMAP and POP3, with STARTTLS or implicit TLS
- Databases: PostgreSQL, MySQL and Redis — a fresh connection and one statement, with an optional expected result. Credentials are encrypted at rest.
- TLS certificate expiry warnings on HTTPS and cert-only checks (one-shot alert inside 14 days)
- Light and dark interface with live WebSocket updates
- Area latency charts (uPlot), uptime timeline bars, per-monitor sparklines, p50/p95/p99
- Named channels with per-event filters and per-monitor attach: email, Slack, Discord, Microsoft Teams, PagerDuty, Opsgenie, Telegram, ntfy, and generic webhooks. PagerDuty and Opsgenie alerts resolve themselves when the monitor recovers.
- SMTP as the mail transport (from Settings)
- Escalation and still-down resend; optional notification rate limit
- API tokens (Bearer / X-Api-Token) and JSON import/export including Uptime Kuma backups
- Monitor groups and dependency suppression (don't page children while the parent is down)
- Environments: named groups of monitors on their own page (cards with average latency). Monitors stays a flat list with an environment / tag / type / status filter bar. A monitor can belong to more than one.
- Maintenance windows (suppress alerts, and optionally announce the work on your status pages)
- Configurable retention (raw / hourly / daily)
- Public status pages (names only; optional branding and custom domains), with embeddable status and uptime badges and an incident RSS feed
- Incident management (auto-open from checks, manual updates)
- Status page subscribers (email confirm + webhook)
- Favicon badge showing down count, responsive layout
- Accounts and roles (admin / operator / viewer), and SSO / OIDC
- White-label branding: custom product name, logo, accent color, and login message
- Distributed probe nodes, with alerts only when enough locations agree
- SLA / SLO reporting with CSV export and a monthly uptime report by email
- Audit log
- Backup and restore (`make backup` / `make restore`)
- Probe dead-man's-switch and stale-check detection
- Prometheus `/api/metrics` and an OpenAPI description at `/api/openapi.json`

The dashboard is virtualized for large fleets. **Hundreds of monitors on a small VPS is the expected range.** A 1,000-monitor SLA is not claimed; ListMonitors is timed in the test suite at 250 rows.

## Quick start

Requirements: Docker + Docker Compose, on `linux/amd64` or `linux/arm64`. The
images build inside Docker, so no Go or Node toolchain is needed on the host.

```bash
git clone https://github.com/zypher-systems/argus.git && cd argus
cp deploy/.env.example deploy/.env   # set the admin password and a real db password
make up                              # builds and starts the full stack
```

Update with `git pull && make up`. A version tag also publishes
`ghcr.io/zypher-systems/argus/backend` and `web` for `linux/amd64` and
`linux/arm64`. Until that tag exists, every install builds from a checkout.
`deploy/docker-compose.release.yml` pulls those images and does not build.

Open http://localhost:8080 (or `ARGUS_WEB_PORT`) and sign in with the admin credentials from your `.env`.

The first boot creates the admin account automatically. Data is stored in the `argus_pgdata` Docker volume. Postgres will not start if `POSTGRES_PASSWORD` is unset.

Operator notes (env vars, backup/restore, upgrades, reverse proxy): [docs/operator.md](docs/operator.md).

Pull requests into `dev` and `main` run on codevault: Go vet and tests, the
linter, the frontend build, and the smoke. Locally, `make test` still runs
the backend in Docker, and `make e2e` runs the smoke against a stack that is
already up, reading the admin password and port from `deploy/.env`.

## The stack

```
Browser ──▶ web (nginx: React app + reverse proxy)
              ├── api   (Go: REST API, WebSocket hub, rollups)
              ├── probe (Go: ICMP scheduler, state machine, alert dispatch)
              └── db    (PostgreSQL 16)
```

- `api` and `probe` share one image (`argus/backend`); the probe ICMP path uses Linux ping sockets so it works as the non-root container user
- Services coordinate via Postgres `LISTEN/NOTIFY` (`argus_config` for reloads, `argus_events` for the live dashboard)
- Migrations run automatically on `api` startup
- TLS for the UI is your reverse proxy if you want HTTPS. Argus does not ship a terminator. Set `ARGUS_COOKIE_SECURE=true` when the UI is served over HTTPS.
- Containers run as non-root. nginx listens on **8080** inside the web container.
- A hosted, multi-organization build exists (`-tags hosted`, `ARGUS_HOSTED`). A self-hosted install ignores that setting. An organization's plan label does not lock features.

## Make targets

| Command | Description |
| --- | --- |
| `make up` | Build + start the source-build stack |
| `make down` | Stop the stack (data kept) |
| `make logs` | Follow logs |
| `make test` | Backend vet + tests (runs in Docker) |
| `make e2e` | Compose smoke (stack must be up on `ARGUS_WEB_PORT`) |
| `make backup` | `pg_dump` into `backups/` |
| `make restore FILE=...` | Restore a dump (replaces the database) |
| `make clean` | Stop the stack and **delete the database** |

## Alert email setup

1. Settings → **Email alerts (SMTP)** — fill in host, port, encryption, credentials
2. Settings → **Notification channels** — add an Email channel (and/or webhook, Slack, Discord)
3. Click **Send test email** / test send per channel to verify delivery

## Development

```bash
make test          # go vet + go test in Docker
make e2e           # smoke against a running stack
docker compose -f deploy/docker-compose.yml build
```

Work on a patch branch (`2.0.0-patch`) and merge to `dev`, then `dev` to `main`.

Frontend: Vite + React + TypeScript + Tailwind v4 (in `frontend/`).
Backend: Go + chi + pgx (in `backend/`).

## History

[CHANGELOG.md](CHANGELOG.md) records every release.

## License

[MIT](LICENSE). Copyright (c) 2026 Zypher Systems.
