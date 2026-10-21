#!/bin/sh
# Compose smoke: health JSON, login, monitor CRUD, escalation, logout.
set -eu

BASE="${ARGUS_BASE_URL:-http://localhost:8080}"
EMAIL="${ARGUS_ADMIN_EMAIL:-admin@example.com}"
PASS="${ARGUS_ADMIN_PASSWORD:?set ARGUS_ADMIN_PASSWORD to the admin password (make e2e reads deploy/.env)}"
COOKIE="$(mktemp)"
trap 'rm -f "$COOKIE"' EXIT

wait_ready() {
  i=0
  while [ "$i" -lt 60 ]; do
    if curl -sf "$BASE/api/health" >/dev/null; then
      return 0
    fi
    i=$((i + 1))
    sleep 2
  done
  echo "smoke: $BASE/api/health never became ready" >&2
  return 1
}

expect_code() {
  want="$1"
  shift
  got="$(curl -s -o /tmp/argus-smoke-body -w '%{http_code}' "$@")"
  if [ "$got" != "$want" ]; then
    echo "smoke: expected HTTP $want, got $got from $*" >&2
    cat /tmp/argus-smoke-body >&2
    return 1
  fi
}

wait_ready
health="$(curl -sf "$BASE/api/health" || true)"
echo "$health" | grep -q '"status":"ok"' || {
  echo "smoke: /api/health did not return JSON status ok: $health" >&2
  exit 1
}
expect_code 200 "$BASE/api/health"
expect_code 200 "$BASE/api/metrics"
expect_code 200 "$BASE/api/branding"
expect_code 200 "$BASE/"

expect_code 200 -c "$COOKIE" -H "Content-Type: application/json" \
  -d "{\"email\":\"$EMAIL\",\"password\":\"$PASS\"}" \
  "$BASE/api/auth/login"

expect_code 200 -b "$COOKIE" "$BASE/api/auth/me"
expect_code 200 -b "$COOKIE" "$BASE/api/monitors"
expect_code 200 -b "$COOKIE" "$BASE/api/settings"

create_body='{"name":"smoke-icmp","type":"icmp","target":"127.0.0.1","interval_seconds":60,"retries":3}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$create_body" "$BASE/api/monitors"
ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
if [ -z "$ID" ]; then
  echo "smoke: create monitor did not return an id" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
fi

expect_code 200 -b "$COOKIE" "$BASE/api/monitors/$ID"

expect_code 200 -b "$COOKIE" "$BASE/api/realms"
realm_body='{"name":"smoke-websites"}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$realm_body" "$BASE/api/realms"
REALM_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
if [ -z "$REALM_ID" ]; then
  echo "smoke: create realm did not return an id" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
fi
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"name":"Smoke-Websites"}' "$BASE/api/realms"
apps_body='{"name":"smoke-apps"}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$apps_body" "$BASE/api/realms"
APPS_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
if [ -z "$APPS_ID" ]; then
  echo "smoke: create second realm did not return an id" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
fi
member_body="{\"name\":\"smoke-realm-member\",\"type\":\"icmp\",\"target\":\"127.0.0.1\",\"interval_seconds\":60,\"retries\":3,\"realm_ids\":[$REALM_ID,$APPS_ID]}"
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$member_body" "$BASE/api/monitors"
MEMBER_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
if [ -z "$MEMBER_ID" ]; then
  echo "smoke: create realm member did not return an id" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
fi
grep -q "\"realm_ids\"" /tmp/argus-smoke-body || {
  echo "smoke: monitor missing realm_ids" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
}
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"name":"smoke-bad-realm","type":"icmp","target":"127.0.0.1","interval_seconds":60,"realm_ids":[999999]}' \
  "$BASE/api/monitors"
expect_code 200 -b "$COOKIE" "$BASE/api/realms"
grep -q "smoke-websites" /tmp/argus-smoke-body || {
  echo "smoke: realms list missing smoke-websites" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
}

http_body='{"name":"smoke-http","type":"http","target":"http://api:8080/api/health","interval_seconds":60,"config":{"method":"GET","expect_status":0}}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$http_body" "$BASE/api/monitors"
HTTP_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

expect_code 200 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"minutes":15}' "$BASE/api/monitors/$HTTP_ID/mute"
grep -q "muted_until" /tmp/argus-smoke-body || {
  echo "smoke: mute missing muted_until" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
}
expect_code 200 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"minutes":0}' "$BASE/api/monitors/$HTTP_ID/mute"
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"minutes":0}' "$BASE/api/monitors/$HTTP_ID/ack"
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{}' "$BASE/api/monitors/$HTTP_ID/clone"
grep -q "smoke-http copy" /tmp/argus-smoke-body || {
  echo "smoke: clone missing copy name" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
}
expect_code 200 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{}' "$BASE/api/monitors/$HTTP_ID/check"

http_post='{"name":"smoke-http-post","type":"http","target":"http://api:8080/api/health","interval_seconds":60,"config":{"method":"POST","body":"{\"ping\":true}","json_path":"status","json_expect":"ok"}}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$http_post" "$BASE/api/monitors"
HTTP_POST_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

get_body='{"name":"smoke-http-get-body","type":"http","target":"http://api:8080/api/health","interval_seconds":60,"config":{"method":"GET","body":"{}"}}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$get_body" "$BASE/api/monitors"

tcp_body='{"name":"smoke-tcp","type":"tcp","target":"api:8080","interval_seconds":60}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$tcp_body" "$BASE/api/monitors"
TCP_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

bad_type='{"name":"smoke-bad","type":"browser","target":"https://example.com","interval_seconds":60}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$bad_type" "$BASE/api/monitors"

# 2.7.0 monitor types. These are created, not checked: the smoke stack has no
# mail or database server, so what is under test is validation and storage.
dns_body='{"name":"smoke-dns","type":"dns","target":"example.com","interval_seconds":60,"config":{"record":"MX","expect_value":"example.com"}}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$dns_body" "$BASE/api/monitors"
DNS_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

dns_bad='{"name":"smoke-dns-bad","type":"dns","target":"example.com","interval_seconds":60,"config":{"record":"SRV"}}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$dns_bad" "$BASE/api/monitors"

domain_body='{"name":"smoke-domain","type":"domain","target":"example.com","interval_seconds":86400,"config":{"warn_days":45}}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$domain_body" "$BASE/api/monitors"
DOMAIN_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

domain_bad='{"name":"smoke-domain-bad","type":"domain","target":"example.com","interval_seconds":86400,"config":{"warn_days":900}}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$domain_bad" "$BASE/api/monitors"

mail_body='{"name":"smoke-smtp","type":"smtp","target":"mail.example.com","interval_seconds":300,"config":{"tls_mode":"starttls","keyword":"ESMTP"}}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$mail_body" "$BASE/api/monitors"
MAILMON_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

mail_bad='{"name":"smoke-smtp-bad","type":"smtp","target":"mail.example.com","interval_seconds":300,"config":{"tls_mode":"ssl"}}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$mail_bad" "$BASE/api/monitors"

# A database monitor's password goes in encrypted and never comes back out.
db_body='{"name":"smoke-postgres","type":"postgres","target":"db:5432","interval_seconds":120,"config":{"database":"argus","auth_user":"argus","auth_password":"smoke-secret-pw","insecure_skip_verify":true}}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$db_body" "$BASE/api/monitors"
DB_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
if grep -q 'smoke-secret-pw' /tmp/argus-smoke-body; then
  echo "smoke: create returned the database password in clear" >&2
  exit 1
fi
if [ -n "$DB_ID" ]; then
  expect_code 200 -b "$COOKIE" "$BASE/api/monitors/$DB_ID"
  if grep -q 'smoke-secret-pw' /tmp/argus-smoke-body; then
    echo "smoke: monitor read returned the database password in clear" >&2
    exit 1
  fi
  grep -q '"auth_user":"argus"' /tmp/argus-smoke-body || {
    echo "smoke: monitor read lost auth_user" >&2
    cat /tmp/argus-smoke-body >&2
    exit 1
  }
fi

redis_bad='{"name":"smoke-redis-bad","type":"redis","target":"cache:6379","interval_seconds":60,"config":{"query":"FLUSHALL"}}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$redis_bad" "$BASE/api/monitors"

redis_body='{"name":"smoke-redis","type":"redis","target":"cache:6379","interval_seconds":60,"config":{"query":"DBSIZE"}}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$redis_body" "$BASE/api/monitors"
REDIS_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

tx_bad='{"name":"smoke-tx-bad","type":"transaction","interval_seconds":60,"config":{"steps":[{"url":"http://api:8080/api/health?t={{token}}"}]}}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$tx_bad" "$BASE/api/monitors"
tx_body='{"name":"smoke-tx","type":"transaction","interval_seconds":300,"config":{"steps":[{"name":"Health","url":"http://api:8080/api/health","keyword":"ok","extract":[{"name":"status","from":"json","path":"status"}]},{"name":"Again","url":"http://api:8080/api/health?s={{status}}&k={{secret.key}}"}],"secrets":{"key":"smoke-tx-secret"}}}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$tx_body" "$BASE/api/monitors"
TX_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
if grep -q 'smoke-tx-secret' /tmp/argus-smoke-body; then
  echo "smoke: transaction create returned its secret" >&2
  exit 1
fi
expect_code 200 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$tx_body" "$BASE/api/monitors/test-run"
grep -q '"success":true' /tmp/argus-smoke-body || {
  echo "smoke: transaction test run failed" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
}

thresh_body='{"name":"smoke-slow","type":"icmp","target":"127.0.0.1","interval_seconds":60,"retries":3,"latency_threshold_ms":250}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$thresh_body" "$BASE/api/monitors"
THRESH_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

thresh_neg='{"name":"smoke-slow-bad","type":"icmp","target":"127.0.0.1","interval_seconds":60,"latency_threshold_ms":-1}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$thresh_neg" "$BASE/api/monitors"

thresh_big='{"name":"smoke-slow-big","type":"icmp","target":"127.0.0.1","interval_seconds":60,"latency_threshold_ms":600001}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$thresh_big" "$BASE/api/monitors"

esc_body='{"name":"smoke-esc","type":"icmp","target":"127.0.0.1","interval_seconds":60,"escalate_minutes":5}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$esc_body" "$BASE/api/monitors"
ESC_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

expect_code 200 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "{\"ids\":[$THRESH_ID,$ESC_ID],\"action\":\"pause\"}" \
  "$BASE/api/monitors/bulk"
grep -q '"updated":2' /tmp/argus-smoke-body || {
  echo "smoke: bulk pause did not update 2" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
}
expect_code 200 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "{\"ids\":[$THRESH_ID,$ESC_ID],\"action\":\"add_tag\",\"tag\":\"smoke\"}" \
  "$BASE/api/monitors/bulk"
expect_code 200 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "{\"ids\":[$THRESH_ID,$ESC_ID],\"action\":\"resume\"}" \
  "$BASE/api/monitors/bulk"

push_body='{"name":"smoke-push","type":"push","target":"cron job","interval_seconds":60}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$push_body" "$BASE/api/monitors"
PUSH_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
PUSH_TOKEN="$(sed -n 's/.*"push_token":"\([^"]*\)".*/\1/p' /tmp/argus-smoke-body | head -n 1)"
if [ -n "$PUSH_TOKEN" ]; then
  expect_code 200 "$BASE/api/push/$PUSH_TOKEN?status=up&msg=ok"
fi

group_body='{"name":"smoke-group","type":"group","target":"","interval_seconds":60}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$group_body" "$BASE/api/monitors"
GROUP_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

expect_code 200 -b "$COOKIE" "$BASE/api/export"
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"passphrase":"short"}' "$BASE/api/export"
expect_code 200 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"passphrase":"smoke passphrase long enough"}' "$BASE/api/export"
grep -q '"argus-encrypted"' /tmp/argus-smoke-body || {
  echo "smoke: encrypted export has the wrong format" >&2
  exit 1
}
expect_code 200 -b "$COOKIE" "$BASE/api/tokens"
tok_body='{"name":"smoke-token"}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$tok_body" "$BASE/api/tokens"
TOK_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
TOK="$(sed -n 's/.*"token":"\([^"]*\)".*/\1/p' /tmp/argus-smoke-body | head -n 1)"
if [ -n "$TOK" ]; then
  expect_code 200 -H "Authorization: Bearer $TOK" "$BASE/api/monitors"
fi

expect_code 200 -b "$COOKIE" "$BASE/api/channels"
ch_body='{"name":"smoke-hook","type":"webhook","dest":"https://example.com/argus-hook","events":["down","recovery"]}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$ch_body" "$BASE/api/channels"
CH_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

ch_mail='{"name":"smoke-mail","type":"email","dest":"ops@example.com","events":["slow"]}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$ch_mail" "$BASE/api/channels"
MAIL_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

ch_bad='{"name":"smoke-mail-bad","type":"email","dest":"https://example.com/x"}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$ch_bad" "$BASE/api/channels"

attach='{"name":"smoke-attached","type":"icmp","target":"127.0.0.1","interval_seconds":60,"channel_ids":[999999]}'
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$attach" "$BASE/api/monitors"

if [ -n "$CH_ID" ]; then
  attach_ok="{\"name\":\"smoke-attached-ok\",\"type\":\"icmp\",\"target\":\"127.0.0.1\",\"interval_seconds\":60,\"channel_ids\":[$CH_ID]}"
  expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
    -d "$attach_ok" "$BASE/api/monitors"
  ATTACH_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
fi

sp_body='{"name":"Smoke status","slug":"smoke-status","headline":"ok","published":true}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$sp_body" "$BASE/api/status-pages"
SP_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
expect_code 200 "$BASE/api/status/smoke-status"
expect_code 200 "$BASE/status/smoke-status"
expect_code 400 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"name":"x","slug":"login"}' "$BASE/api/status-pages"
draft='{"name":"Draft","slug":"smoke-draft","published":false}'
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$draft" "$BASE/api/status-pages"
DRAFT_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
expect_code 404 "$BASE/api/status/smoke-draft"

expect_code 200 -b "$COOKIE" "$BASE/api/maintenance-windows"
now_iso="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
soon_iso="$(date -u -d '+2 hours' +%Y-%m-%dT%H:%M:%SZ 2>/dev/null || date -u -v+2H +%Y-%m-%dT%H:%M:%SZ)"
mw_public="{\"starts_at\":\"$now_iso\",\"ends_at\":\"$soon_iso\",\"note\":\"smoke announced window\",\"public\":true}"
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$mw_public" "$BASE/api/maintenance-windows"
MW_PUB_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"
mw_private="{\"starts_at\":\"$now_iso\",\"ends_at\":\"$soon_iso\",\"note\":\"smoke internal window\",\"public\":false}"
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$mw_private" "$BASE/api/maintenance-windows"
MW_PRIV_ID="$(sed -n 's/.*"id":\([0-9][0-9]*\).*/\1/p' /tmp/argus-smoke-body | head -n 1)"

expect_code 200 "$BASE/api/status/smoke-status"
grep -q 'smoke announced window' /tmp/argus-smoke-body || {
  echo "smoke: public status page missing the announced maintenance window" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
}
if grep -q 'smoke internal window' /tmp/argus-smoke-body; then
  echo "smoke: public status page leaked an unannounced maintenance window" >&2
  exit 1
fi

expect_code 200 -b "$COOKIE" "$BASE/api/incidents"
inc_body="{\"title\":\"smoke outage\",\"impact\":\"major\",\"status\":\"investigating\",\"message\":\"looking into it\",\"monitor_id\":$ID}"
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d "$inc_body" "$BASE/api/incidents"
INC_ID="$(grep -o '"id":[0-9][0-9]*' /tmp/argus-smoke-body | head -n 1 | cut -d: -f2)"
if [ -n "$INC_ID" ]; then
  expect_code 200 -b "$COOKIE" "$BASE/api/incidents/$INC_ID"
  expect_code 200 -b "$COOKIE" -H "Content-Type: application/json" \
    -d '{"status":"resolved","message":"recovered"}' "$BASE/api/incidents/$INC_ID/updates"
fi
expect_code 200 -b "$COOKIE" "$BASE/incidents"
expect_code 200 "$BASE/api/status/smoke-status"
grep -q '"incidents"' /tmp/argus-smoke-body || {
  echo "smoke: public status missing incidents" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
}
expect_code 404 "$BASE/api/status-by-host"
expect_code 200 "$BASE/api/openapi.json"
grep -q '"openapi"' /tmp/argus-smoke-body || { echo "smoke: openapi.json is not a spec" >&2; exit 1; }
expect_code 201 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"name":"x","slug":"custom-domain","custom_domain":"status.example.com"}' "$BASE/api/status-pages"
expect_code 200 -b "$COOKIE" "$BASE/api/status-pages/$SP_ID/subscribers"
expect_code 400 -H "Content-Type: application/json" \
  -d '{"email":"ops@example.com"}' "$BASE/api/status/smoke-status/subscribe"

[ -n "$MEMBER_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$MEMBER_ID"
[ -n "$REALM_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/realms/$REALM_ID"
[ -n "$APPS_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/realms/$APPS_ID"
expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$ID"
[ -n "$ESC_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$ESC_ID"
[ -n "$PUSH_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$PUSH_ID"
[ -n "$GROUP_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$GROUP_ID"
[ -n "$TOK_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/tokens/$TOK_ID"
[ -n "$HTTP_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$HTTP_ID"
[ -n "$HTTP_POST_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$HTTP_POST_ID"
[ -n "$TCP_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$TCP_ID"
[ -n "$DNS_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$DNS_ID"
[ -n "$DOMAIN_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$DOMAIN_ID"
[ -n "$MAILMON_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$MAILMON_ID"
[ -n "$DB_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$DB_ID"
[ -n "$REDIS_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$REDIS_ID"
[ -n "${TX_ID:-}" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$TX_ID"
[ -n "$MW_PUB_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/maintenance-windows/$MW_PUB_ID"
[ -n "$MW_PRIV_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/maintenance-windows/$MW_PRIV_ID"
[ -n "$THRESH_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$THRESH_ID"
[ -n "$ATTACH_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/monitors/$ATTACH_ID"
[ -n "$CH_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/channels/$CH_ID"
[ -n "$MAIL_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/channels/$MAIL_ID"
[ -n "$SP_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/status-pages/$SP_ID"
[ -n "$DRAFT_ID" ] && expect_code 200 -b "$COOKIE" -X DELETE "$BASE/api/status-pages/$DRAFT_ID"
expect_code 200 -b "$COOKIE" "$BASE/api/users"
expect_code 403 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"email":"ops@example.com","role":"operator","password":"abcdefgh"}' "$BASE/api/users"
expect_code 403 -b "$COOKIE" "$BASE/api/sla"
expect_code 403 -b "$COOKIE" "$BASE/api/audit"
expect_code 200 -b "$COOKIE" "$BASE/api/instance"
expect_code 200 "$BASE/api/auth/oidc"
expect_code 200 -b "$COOKIE" "$BASE/api/probe-nodes"
expect_code 403 -b "$COOKIE" -H "Content-Type: application/json" \
  -d '{"name":"smoke-east","region":"us-east"}' "$BASE/api/probe-nodes"

expect_code 200 "$BASE/api/auth/hosted"
grep -q '"hosted":false' /tmp/argus-smoke-body || {
  echo "smoke: expected self-hosted hosted=false" >&2
  cat /tmp/argus-smoke-body >&2
  exit 1
}
expect_code 403 -H "Content-Type: application/json" \
  -d '{"email":"a@example.com","password":"abcdefgh","org_name":"Smoke"}' "$BASE/api/auth/register"
expect_code 200 -b "$COOKIE" "$BASE/api/org"
expect_code 200 -b "$COOKIE" -X POST "$BASE/api/auth/logout"

echo "smoke ok"
