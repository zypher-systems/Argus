# Operator guide

How to install, back up, upgrade, and put Argus behind a reverse proxy.
Application behaviour lives in the README; this file is for running the stack.

## Install

The usual install is the published images. Docker and Compose are enough.
The README has the short version: save `docker-compose.yml` and `.env.example`
from the repository, set the passwords in `.env`, and run `docker compose up -d`.

```bash
mkdir argus && cd argus
curl -fsSL -o docker-compose.yml https://raw.githubusercontent.com/zypher-systems/Argus/main/docker-compose.yml
curl -fsSL -o .env.example https://raw.githubusercontent.com/zypher-systems/Argus/main/.env.example
cp .env.example .env
# set POSTGRES_PASSWORD, ARGUS_ADMIN_EMAIL, ARGUS_ADMIN_PASSWORD
docker compose up -d
```

Open `http://localhost:8080` (or `ARGUS_WEB_PORT`). The first boot creates the
admin from `ARGUS_ADMIN_EMAIL` / `ARGUS_ADMIN_PASSWORD`. Argus will not start
for the first time without them: the password must be at least 8 characters
and not the example value. If the api container restarts, `docker compose logs api`
says which of the two to fix.

`latest` is what `docker-compose.yml` pulls. To pin this version, change both
image tags in that file from `latest` to `v2.16.0`.

### From a checkout

`make up` builds `argus/backend` and `argus/web` from this tree and starts
`deploy/docker-compose.yml`. Copy `deploy/.env.example` to `deploy/.env` and
set the same passwords first. Use that path to work on Argus. An install that
only runs it should use the published images above.

`deploy/docker-compose.release.yml` is the same image stack, with the image
names taken from `ARGUS_IMAGE_BACKEND` and `ARGUS_IMAGE_WEB` instead of being
written in the file.

## Environment variables

| Variable | Required | Default | Notes |
| --- | --- | --- | --- |
| `POSTGRES_USER` | yes | — | Database user. Compose passes it to the api and probe as `ARGUS_DB_USER`. |
| `POSTGRES_PASSWORD` | yes | — | Compose refuses to start if unset. Any characters are fine. Postgres only reads it when the data volume is first created: changing it later needs `ALTER USER` in the database as well, or the api can no longer connect. |
| `POSTGRES_DB` | yes | — | Database name. |
| `ARGUS_ADMIN_EMAIL` | first boot | empty | Bootstrap admin, stored in lower case. Ignored after the user exists. |
| `ARGUS_ADMIN_PASSWORD` | first boot | empty | Bootstrap password: at least 8 characters, not the example value. Change it from Settings. |
| `ARGUS_WEB_PORT` | no | `8080` | Host port mapped to nginx (container port 8080). |
| `ARGUS_COOKIE_SECURE` | no | off (on when `ARGUS_HOSTED` unless set) | Set `true` when the UI is served over HTTPS. Also on for any request your proxy marks `X-Forwarded-Proto: https`, which the bundled web container passes through. |
| `ARGUS_HOSTED` | hosted build only | off | Multi-org hosted mode. Public registration creates a new organization and admin. Tenant queries are scoped by org. Ignored, with a warning, by the published images: only a build with `-tags hosted` honours it. |
| `ARGUS_HOSTED_PLAN` | hosted build only | `community` | Label stored on orgs created by registration (`community` or `pro`). It does not lock features. |
| `ARGUS_API_URL` | remote probe | empty | Base URL a remote probe uses to fetch jobs (`https://argus.example.com`). |
| `ARGUS_PROBE_TOKEN` | remote probe | empty | Token from Settings → Probe nodes. When set, `argus probe` talks to the API instead of the database. |
| `ARGUS_DB_HOST`, `ARGUS_DB_USER`, `ARGUS_DB_PASSWORD`, `ARGUS_DB_NAME` | api/probe | set by compose | The database, one value each, so the password needs no escaping. `ARGUS_DB_HOST` may carry a port (default 5432); `ARGUS_DB_SSLMODE` defaults to `disable`. |
| `ARGUS_DB_URL` | no | empty | A whole connection URL, for an external database. Takes precedence over the separate values; a password in it must be URL-encoded. |
| `ARGUS_LISTEN` | no | `:8080` | API bind address inside the container. |
| `ARGUS_PROBE_LISTEN` | no | `:8081` | Probe health bind address (`GET /health`). |
| `ARGUS_IMAGE_BACKEND` | release compose only | — | Image the api and probe run, for `deploy/docker-compose.release.yml`. The root `docker-compose.yml` already uses `ghcr.io/zypher-systems/argus/backend`. |
| `ARGUS_IMAGE_WEB` | release compose only | — | Image nginx runs, for `deploy/docker-compose.release.yml`. The root file already uses `ghcr.io/zypher-systems/argus/web`. |
| `ARGUS_SECRET_KEY` | recommended | — | 64 hex characters. Encrypts monitor credentials at rest. Set it on api **and** probe, and back it up. |
| `ARGUS_TRUSTED_PROXIES` | behind a proxy | `1` | Reverse proxies in front of the API, counting the bundled web container. Set `2` behind nginx, Caddy or Traefik. See [Reverse proxy](#reverse-proxy). |

## First run

After signing in, the Overview shows **Get started** to admins and operators:
watch something, choose where alerts go, send a test alert, publish a status
page. Each step ticks itself when the thing exists, whether it was done from
the checklist or elsewhere, and the list goes away once all four are done.
**Hide** removes it sooner; **Show getting started** at the bottom of the
Overview brings it back while steps remain.

To start watching, paste URLs or hosts one per line: a URL becomes an HTTP
check, `host:port` a TCP check, anything else a ping. Or import an Uptime
Kuma backup. "Email me" appears once SMTP is set up under Settings → Alerts.

## Monitor types

| Type | Watches | Notes |
| --- | --- | --- |
| ICMP | A host answers ping | |
| HTTP | A URL | Status, keyword, JSON path, auth, proxy. Every check opens a new connection, so latency includes connecting and TLS. A `Host` header is sent as the request's host. Redirects are followed unless the expected status is itself a redirect (301, 302…) |
| TCP | A port accepts connections | |
| DNS | A record resolves | A, AAAA, CNAME, MX, NS, TXT, with an expected answer (TXT matches a substring) |
| TLS certificate | Certificate expiry | Warns once per expiry date, 14 days ahead unless the monitor sets its own window. Behind a pool whose machines hold different certificates, each is warned about once |
| Domain | Registration expiry over RDAP | Warns 30 days ahead by default. Not the same as the certificate |
| SMTP / IMAP / POP3 | A mail server greets | Plain, STARTTLS or implicit TLS; reads certificate expiry; optional banner keyword |
| PostgreSQL / MySQL | A fresh connection and one statement | Defaults to `SELECT 1`; optional query and expected result |
| Redis | A fresh connection and one command | Defaults to `PING`; commands are limited to reads |
| Transaction | A sequence of HTTP requests | Up to 10 steps sharing cookies; values carried between steps; the failing step is named. See [Transaction monitors](#transaction-monitors) |
| Push | A cron or backup job checks in | |
| Group | Nothing; groups other monitors | |

Database and mail credentials, and transaction secrets, are encrypted at rest
when `ARGUS_SECRET_KEY` is set (see below), and are never returned by the API.

## Scheduled heartbeats

A push monitor normally expects a ping every check interval. For a job that
runs at set times, such as a nightly backup, choose **On a schedule** and give a
cron expression and a time zone:

| Expression | Means |
| --- | --- |
| `0 2 * * *` | every day at 02:00 |
| `30 6 * * 1-5` | weekdays at 06:30 |
| `0 */4 * * *` | every four hours, on the hour |
| `@daily`, `@hourly`, `@weekly` | the obvious |

After each ping, the next one is due at the schedule's next time. **Grace** is
how long the job may run after its start time before a missing ping counts as
down; it is 5 minutes unless you set it. Have the job ping when it finishes,
successfully:

```bash
pg_dump … && curl -fsS "https://argus.example.com/api/push/<token>?status=up"
```

The form previews the next three times as you type, and the monitor shows
when the next ping is due. Scheduled heartbeats are checked every minute.

A ping that arrives slightly before its time counts for that time: up to a
tenth of the gap between runs, and never more than five minutes, so a job on
a machine whose clock runs a few seconds fast is not marked late. On the
night clocks go back, a job scheduled inside the repeated hour is expected
once, as cron runs it.

## Transaction monitors

A transaction walks through an application the way a user or client does: log
in, take the token that comes back, and use it. A plain HTTP check shows the
homepage answering. A transaction shows that logging in, and the requests
after it, still work.

- **Steps** run in order, up to 10. Each has a method, URL, optional headers
  and body, an expected status (0 accepts any 2xx or 3xx), and optional text
  the response must contain. The first failing step stops the run, and the
  alert names it: `step 2 (List orders): status 500`.
- **Cookies** are shared across the steps of one run and discarded after it,
  so a session login carries through without any setup.
- **Extracting values.** A step can capture a value from its response, either
  from the JSON body with a dotted path (`data.token`, or `items.0.id` for
  the first array element) or from a response header. Later steps use it as
  `{{token}}` in their URL, headers or body. A reference that has not been
  extracted yet is rejected when you save, and a request is never sent with
  a placeholder still in it.
- **Secrets** are named values such as a password or API key, written as
  `{{secret.password}}`. They are encrypted at rest with `ARGUS_SECRET_KEY`,
  and the API returns their names but never their values. When you edit a
  monitor, leaving a secret blank keeps the stored value.
- **Evidence.** The last failure lists every step with its status and time,
  and keeps the failing step's response body. Secret and extracted values are
  replaced with `[redacted]` wherever they would appear: the URL, the curl
  line, the body and the error.
- **Timeout** covers the whole transaction and defaults to 30 seconds.
- **Test run.** The editor's Test run button runs the steps once as they stand,
  without saving, and shows how each step went. It runs from the Argus server
  rather than a remote probe, uses the stored secrets for any left blank while
  editing, and is limited to 10 runs a minute per user.
- **Content-Type.** A body without a Content-Type header is sent as JSON, form
  data or plain text, depending on what it looks like.

Use a dedicated account for the monitor with the least access that exercises
the flow, and avoid steps that create data on every run. The check runs as
often as its interval says. Many applications, Argus included, end an
account's other sessions when it signs in, so a transaction using your own
account signs you out on every run.

Secrets are protected from being read, not from being used: anyone who can
edit a transaction can point a step at a server of their choosing and send it
the secret. Give editing rights accordingly.

## Emails

Every email Argus sends (alerts, test messages, status page updates,
subscription confirmations, and the monthly report) uses one light layout and
carries a plain-text version for pagers and text-only clients. Set **Base
URL** under Settings → Data so alerts link back to the monitor and
subscriber emails can carry an unsubscribe link; without it Argus cannot know
its own address. A From address may include a name: `Argus <alerts@example.com>`.

Status page subscribers can unsubscribe from any email: a link to a page that
asks before acting, and the `List-Unsubscribe` one-click headers that Gmail
and Yahoo expect.

## Credential encryption

Set `ARGUS_SECRET_KEY` to 64 hex characters (`openssl rand -hex 32`) on both
the api and the probe. It encrypts monitor credentials — database passwords and
HTTP auth secrets and transaction secrets — inside the JSONB column, so a
database dump is not a credential dump. An export is different: it is a
restorable backup, so it carries credentials in clear unless you give it a
passphrase. See [Export](#export).

- **Back it up with your database backups.** Without it, those credentials cannot
  be decrypted; Argus logs a clear error and the affected checks fail to
  authenticate until the secrets are re-entered.
- Leaving it unset keeps the old behaviour: credentials are stored as-is.
- Existing monitors keep working; each is encrypted the next time it is saved.

## Maintenance windows

Settings → Alerts → Maintenance windows. A window suppresses down, recovery and
expiry alerts, and keeps incidents off status pages. Tick **Announce on status
pages** to also show it publicly: anything running now, and anything starting
within the next two weeks, appears above the service list with its times.

Suppressed is not the same as dropped. Argus keeps track of what a
monitor's recipients were last told, and within 30 seconds of a window
ending sends what that leaves owed:

- A monitor that went down during the window and is still down is alerted
  then, with its incident. The alert says it was delayed.
- A monitor whose outage was alerted before the window and which recovered
  during it gets its recovery, so the alert it opened in PagerDuty or
  Opsgenie is closed.
- An outage that begins and ends inside the window sends nothing.
- A monitor that was reported down, recovered and failed again inside the
  window is not alerted a second time, since its recipients' last word is
  already "down", but the new outage gets its incident.

The same holds for a muted monitor and for one whose `depends_on` parent is
down. Pausing a monitor changes nothing that is owed, and nothing is sent
while it is paused. When it is switched back on, Argus waits for its first
check before acting on its status: a monitor paused while down with nobody
told is reported if that check finds it still down, and one that had been
reported down gets its recovery when it is up.

Deleting a monitor that was reported down sends its recovery, saying it was
deleted, so the alert does not stay open on a pager.

## Notification channels

Settings → Alerts → Notification channels. Each channel picks the events it
receives; attach them per monitor or leave the default set.

| Type | What goes in the destination |
| --- | --- |
| Email | Comma-separated addresses. Uses the SMTP settings above. |
| Slack | Incoming webhook URL (Apps → Incoming Webhooks). |
| Discord | Channel settings → Integrations → Webhooks. |
| Microsoft Teams | A channel connector URL, or a Power Automate flow that posts to the channel. |
| PagerDuty | The Events API v2 integration key. Add `@eu` (`key@eu`) for the EU service region. |
| Opsgenie | An API integration key. Add `@eu` for the EU region. |
| Telegram | `<bot token>:<chat id>` — the token from @BotFather, then the chat the bot posts to. |
| ntfy | The topic URL, for example `https://ntfy.sh/argus-alerts`. For a protected topic use `https://user:password@host/topic`. |
| Webhook | Any URL. Argus POSTs `{event, message, monitor, at, detail}`. |

PagerDuty and Opsgenie are incident-aware: a `down` opens an alert keyed to the
monitor and a `recovery` closes that same alert, so your on-call view does not
fill with duplicates. Reminders and escalations for the same outage join it.
A slow monitor, an expiring certificate and an expiring domain each have a
key of their own (`argus-monitor-<id>-slow`, `-cert`, `-domain`), so an
outage that starts while one of those is open still pages. A channel test
uses `argus-test`.

Alerts are queued in the database before they are sent, one entry per
channel. If a channel cannot be reached the alert is retried, after 5 and 15
seconds, 1, 5 and 15 minutes, then every 30 minutes, for up to a day; the
monitor's alert history shows *retrying* and then *sent* or *failed*. A
restart of the api or the probe loses nothing: whichever is running picks the
queue up. Channels are sent to side by side, so a mail server that hangs does
not delay the pager; a channel that hangs has at most two alerts in flight to
it at once, however many are waiting.

Alerts about one problem reach a channel in the order they happened: a
recovery never arrives before the outage it ends. If the monitor recovers
while its outage alert is still waiting, that alert is dropped and only the
recovery is sent; if it goes down again while a recovery is still waiting,
the recovery is dropped and the new outage is sent. An alert already being
sent is left to finish, and the next one follows it. A recovery goes to every
channel the outage went to, including one you have taken off the monitor
since. Escalations and reminders wait until the outage itself has been
reported. Delivery is at least
once: if Argus stops after a provider accepted an alert and before recording
that, the alert is sent again two minutes later.

With a notification rate limit set (Settings → Alerts), reminders and
warnings over the limit are skipped. A monitor going down or coming back,
or coming back under its latency threshold, is never skipped: it waits and
is sent as soon as the hour has room. The limit is a brake on floods, not an
exact count: a few alerts sent at the same moment can pass it together.

Deleting a channel takes it off the monitors attached to it. A monitor left
with no channel goes back to the default set, every channel, so it is never
left alerting nobody.

Use **Test** on a channel to send one now; a failure shows the provider's own
message.

## Status badges and feeds

Every published status page exposes:

- `GET /api/status/<slug>/badge.svg` — overall status
- `GET /api/status/<slug>/uptime.svg` — 90-day uptime across the page
- `GET /api/status/<slug>/feed.xml` — incident history as RSS

Badges take an optional `?label=`. They cache for a minute, the feed for five.
The Status pages screen has copy buttons for all three.

```markdown
![status](https://status.example.com/api/status/public/badge.svg)
```

## Monthly uptime report

Settings → Alerts → Monthly uptime report. On the first of each month at
08:00 UTC, Argus emails the recipients an uptime summary for the month that
just ended: every monitor, its uptime
against its SLO target, and which ones missed. **Send last month now** sends
the same email immediately so you can see it before committing to it. Running
two api containers is safe: the month is claimed once.

## API and automation

`GET /api/openapi.json` describes the monitoring API (monitors, channels,
incidents, reports and the public status endpoints). Settings, users,
tokens and status pages are not in it yet. It needs no authentication, so
generators and Swagger UI can read it before they have a token.

Authenticate automation with an API token (Settings → Access → API tokens):

```bash
curl -H "Authorization: Bearer $ARGUS_TOKEN" https://argus.example.com/api/monitors
```

`X-Api-Token: <token>` works too.

## Push monitors and API tokens

Push / heartbeat monitors expose `GET` or `POST /api/push/{token}` (no session
cookie). Cron jobs should hit the URL at least once per check interval. Set Base
URL in Settings so the dashboard can show the full URL.

A job can report its own failure with `?status=down` (and `&msg=…`). The
monitor then counts as failing every time it is looked at, until a heartbeat
without `status=down` arrives, and goes down after its retries like any
other.

API tokens are created in Settings. Send `Authorization: Bearer <token>` or
`X-Api-Token`. Tokens are shown once. Each token acts as the user who created it.

## Teams, SSO, and remote probes

Additional users, OIDC, distributed probes, SLA reports, and the audit log
are part of every install.

OIDC redirect URI is `{base_url}/api/auth/oidc/callback`. Set Base URL in
Settings to the public origin.

Single sign-on creates an account the first time someone signs in. It joins
an account that already exists only when the identity provider states the
email address is verified (`email_verified`). Where it does not, as with
Microsoft Entra ID, a person who already has a password account is refused
at single sign-on: remove that account and let their first sign-in create a
new one. The check is what stops someone who can set their own email at the
provider from signing in as an existing administrator.

A saved monitor credential is reused only for the host it was saved for.
Changing a monitor's host, proxy or type with the password, token or a
transaction secret left blank is refused: enter it again.

Roles:

| Role | Can |
| --- | --- |
| Viewer | Read everything except secrets. Channel destinations are masked, since a webhook URL or integration key is the means to send an alert. |
| Operator | Manage monitors, channels, incidents, status pages and maintenance. Not users, exports, probe nodes, or anything in Settings that decides where mail and sign-in go (mail server, single sign-on, base URL, branding, retention, reports). |
| Admin | Everything, including exports, which contain every credential. |

### Sign-in protection

- Five attempts per 15 minutes from one address (see `ARGUS_TRUSTED_PROXIES`).
- Twenty failed attempts on one account within 15 minutes, from any mix of
  addresses, closes that account to addresses that have not signed into it
  in the last 30 days. Addresses the owner normally uses still work, so the
  limit cannot be used to lock you out of your own monitoring. Emails that
  have no account are limited the same way, so the limit reveals nothing.
- A wrong password takes the same time whether or not the account exists.

## Hosted multi-org

One compose stack can serve many organizations. This needs the hosted build
(`docker build --build-arg GO_TAGS=hosted backend`); the published images are
self-hosted builds and ignore `ARGUS_HOSTED`. Self-hosted installs keep
everyone in the Default org.

With the hosted build and `ARGUS_HOSTED=true`:

- `GET /api/auth/hosted` reports `{hosted, registration}`.
- `POST /api/auth/register` creates a new org plus an admin (rate-limited like login).
- Authenticated API queries are scoped to the session user's org. The probe
  engine still sees every monitor on the cluster.
- Emails and status-page slugs stay globally unique.
- New orgs get `ARGUS_HOSTED_PLAN` (default `community`). The label is stored
  on the organization and does not lock features.
- Session cookies default to `Secure` unless `ARGUS_COOKIE_SECURE=false`.

Do not enable hosted mode on HTTP without `ARGUS_COOKIE_SECURE=false` or
browsers will drop the session cookie.

To run a probe in another region, start the published backend image in probe mode:

```bash
docker run -d --restart unless-stopped --cap-add NET_RAW \
  -e ARGUS_API_URL=https://argus.example.com \
  -e ARGUS_PROBE_TOKEN=probe_... \
  ghcr.io/zypher-systems/argus/backend:latest probe
```

Build the probe from the same version as the server.

### Checking from several locations

On the monitor form, under *SLO and probes*, **Check from** lists this server
and every probe node. A monitor with nothing chosen is checked from this
server alone. Push and group monitors are always local.

- **Each location counts its own failures** against the monitor's *failures
  before alert*. Three failures spread over three regions are not three
  failures from one.
- **Mark down when** sets how many locations must agree: any one, some, or
  all of them. With *2 of 3*, one region's network trouble shows on the
  monitor's Locations table but pages nobody.
- **A location that stops reporting drops out.** A probe node counts toward
  the required agreement while it keeps asking for work (every 30 seconds).
  If it goes quiet for two minutes it stops counting, so a dead node can
  neither hold a monitor down nor stop an outage being confirmed. The
  Locations table marks it *not reporting*.
- **A silent node is alerted.** A monitor checked only from a node that has
  stopped gets no results and keeps its last status. After three minutes
  without contact, a node with monitors assigned sends a *probe node down*
  alert to the channels that take probe events, naming the node and how many
  monitors it leaves unchecked, and a recovery when it returns.
- **Deleting a node** takes it off its monitors. One left with no location
  is checked from this server.
- **Alerts say where**: `down from 2 of 3 locations (Frankfurt, Virginia):
  status 503`.
- **Check now** runs on this server. For a monitor that this server does not
  check, the result is shown but not counted.
- Uptime and the latency chart include every check from every location, so
  a region's partial failures show up in uptime even when they never took
  the monitor down.

Remote probe nodes run each monitor on its own interval, with up to 16 checks
at a time, and fetch their job list every 30 seconds, so a change reaches
them within half a minute.

JSON export/import is Settings → Import / export. Uptime Kuma JSON backups
map ping/http/port/dns/push/group; other Kuma types are skipped.

## Backup

The database volume is `argus_pgdata`. Dump it without stopping the stack:

```bash
make backup
# writes backups/argus-YYYYMMDD-HHMMSS.dump (pg_dump custom format)
```

Keep the dump off the host that runs Argus. The `backups/` directory is
gitignored.

### Export

Settings → Import / export downloads monitors and channels as JSON, which
imports into another Argus. Unlike a database dump, an export holds every
credential in clear, including database passwords, transaction secrets and
webhook URLs. So:

- only admins can export, and every export is recorded in the audit log;
- give it a passphrase (12 characters or more) and it is encrypted with
  scrypt and AES-256-GCM. Importing needs the same passphrase. There is no
  recovery if it is lost.

Over the API: `GET /api/export` for plain, `POST /api/export` with
`{"passphrase": "…"}` for encrypted, and `POST /api/import` with
`{"format": "argus", "data": <export>, "passphrase": "…"}`.

## Restore

Restore **replaces** the current database. API and probe are stopped for the
duration so `pg_restore` is not blocked by open connections.

```bash
make restore FILE=backups/argus-20260908-120000.dump
```

If the dump was taken from a different major Postgres version, restore may
fail; stay on Postgres 16.

## Upgrade

1. Back up first. From the image install, `docker compose exec -T db sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > argus.dump`. From a checkout, `make backup`.
2. `docker compose pull && docker compose up -d`. A checkout that builds its own images uses `git pull` and `make up` instead. To stay on one version, set the image tags to that version (`v2.16.0`) before pulling.
3. The api runs migrations on startup. `docker compose logs -f api` (or `make logs`) until `/api/health` returns `{"status":"ok"}`.
4. If a migration fails, restore the dump from step 1 and send the log to
   support@zyphersystems.com.

Do not skip the backup. Migrations are not guaranteed to reverse.

## Health

| Service | Check |
| --- | --- |
| api | `GET /api/health` → `{"status":"ok"}` after a Postgres ping; includes `probe.ok` |
| probe | `GET /health` on `:8081` → `{"status":"ok"}` after a Postgres ping |
| metrics | `GET /api/metrics` Prometheus text (`argus_monitors`, `argus_probe_up`, `argus_db_up`) |
| web | `GET /` on `:8080` (nginx) |
| db | `pg_isready` |

Compose marks the container unhealthy if these fail. A stale probe heartbeat
fires `probe_down` to notification channels and a banner in the UI. Monitors stay
at last-known status until the probe process recovers.

## Resource limits and logs

Each service has a memory cap and json-file log rotation (`max-size` 10m,
`max-file` 3). Raise `mem_limit` in compose if Postgres or the API is OOM
killed under a large fleet. Containers run as non-root (`65532` for Go,
`nginx` for the frontend). Probe keeps `NET_RAW` for ICMP.

## Reverse proxy

Argus does not terminate TLS. Put HTTPS in front and set `ARGUS_COOKIE_SECURE=true`.
Forward `Host`, `X-Real-IP`, `X-Forwarded-For`, and `X-Forwarded-Proto`.
WebSockets need an upgrade on `/api/ws`. Argus pings that connection every 25
seconds, so a proxy idle timeout of 30 seconds or more keeps it open; the
defaults in nginx, Caddy, Traefik, and Cloudflare are all longer.

**Set `ARGUS_TRUSTED_PROXIES=2`** when a proxy sits in front of the web
container, and add one more for each further layer (a CDN in front of that
proxy makes 3). Argus rate-limits logins and records the caller's address in
the audit log. Every proxy appends to `X-Forwarded-For`, but the caller can
put anything they like at the start of it, so Argus counts in from the right
by exactly the number of proxies you declare:

| Layout | `ARGUS_TRUSTED_PROXIES` |
| --- | --- |
| Browser → web container (`ARGUS_WEB_PORT`) | `1` (default) |
| Browser → nginx / Caddy / Traefik → web container | `2` |
| Browser → CDN → proxy → web container | `3` |

Too low, and everyone appears to come from your proxy and shares one login
limit. Too high, and callers can choose their own address. A higher value is
only safe when `ARGUS_WEB_PORT` cannot be reached except through the proxy.
Bind it to `127.0.0.1` or firewall it.

### nginx

```nginx
server {
    listen 443 ssl;
    server_name status.example.com;
    # ssl_certificate / ssl_certificate_key ...

    location /api/ws {
        proxy_pass http://127.0.0.1:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
        proxy_set_header Host $host;
        proxy_set_header X-Forwarded-Proto $scheme;
        proxy_read_timeout 3600s;
    }

    location / {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }
}
```

### Caddy

```caddy
status.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

Caddy sets `X-Forwarded-Proto` and handles WebSocket upgrades by default.

### Traefik

```yaml
labels:
  - traefik.enable=true
  - traefik.http.routers.argus.rule=Host(`status.example.com`)
  - traefik.http.routers.argus.tls=true
  - traefik.http.services.argus.loadbalancer.server.port=8080
```

Point the router at the **web** container (or the published `ARGUS_WEB_PORT`),
not the API port.

## Custom domains

Status pages can bind a hostname (`status.example.com`). Argus does not
issue certificates. CNAME the name at the Argus host and terminate TLS on this
reverse proxy. Forward `Host` so `GET /api/status-by-host` can match the page.
Do not set the custom domain to the same hostname as the admin UI.

Visitors can subscribe by email; webhook subscribers are added on the status
page edit form.

Email subscribe needs SMTP configured (Settings). Confirmation lands on
`/subscribe/confirm?token=…`.

## Support

Write to **support@zyphersystems.com**. Say which Argus version you run.
The instance ID is available from the API at `GET /api/instance`.

Administrators can also use **Send feedback** in the sidebar. A bug or an
idea opens a GitHub issue with the Argus version, the page, and the browser
filled in. It does not include the instance ID. Anything else opens a draft
in your own email app, and that draft does include the instance ID. Argus
does not send either one for you.

## Troubleshooting

**Compose exits: `set POSTGRES_PASSWORD in .env`.** Copy `.env.example` to `.env` and set a real password. A source-build stack (`make up`) says `deploy/.env` instead; copy `deploy/.env.example` there.

**The stack starts but the api container keeps restarting.** `make logs`
shows why. On a first start it is usually the administrator: "no account
exists yet and the first one cannot be created" names the variable to fix.
"database connection failed" after changing `POSTGRES_PASSWORD` means the
database still has the old one; see the variable's note above.

**Forgotten password.** `ARGUS_ADMIN_PASSWORD` is only read on the first
start, and there is no reset by email. Set a new one from the server:

```bash
docker compose -f deploy/docker-compose.yml exec api argus reset-password you@example.com
```

It asks for the new password (at least 8 characters) and signs that account
out everywhere.

**`/api/health` is 503.** Postgres is down or unreachable. `docker compose ps`
and `make logs` on `db` / `api`.

**Login works on HTTP but cookies vanish on HTTPS.** Set `ARGUS_COOKIE_SECURE=true`
and make sure the proxy sends `X-Forwarded-Proto: https`.

**ICMP monitors stay pending / down while `ping` from the host works.** The
probe process is UID 65532. Docker grants `NET_RAW` to root in the container,
not to that user, so raw ICMP sockets fail with "operation not permitted".
2.2.1 uses Linux ping sockets instead. `NET_RAW` stays in compose as a
fallback. Confirm with `docker exec argus-probe-1 cat /proc/1/status` —
`CapEff` is normally zero; that is expected.

**arm64 `exec format error`.** The image was built for another architecture.
Confirm with `docker buildx imagetools inspect <image>`, and build on the
machine that will run it.

**Web container unhealthy after upgrade.** nginx now listens on **8080** inside
the container (non-root). Host mapping is `ARGUS_WEB_PORT:8080`, not `:80`.
