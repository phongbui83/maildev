#!/usr/bin/env bash
# Run the full edition's end-to-end suites against the lite binary. The binary
# is started in dev auth mode with multi-project enabled (MAILDEV_EDITION=full)
# so the unchanged scripts apply; lite_test.py then covers the lite specifics.
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
backend=${BACKEND:-$here/../backend}
tmp=$(mktemp -d)
trap 'kill $pid 2>/dev/null || true; rm -rf "$tmp"' EXIT
(cd "$here" && go build -o "$tmp/maildev-lite" ./cmd/maildev-lite)
common="MAILDEV_COOKIE_SECURE=false MAILDEV_HOSTNAME=localhost MAILDEV_PUBLIC_URL=http://localhost:18080 MAILDEV_HTTP_ADDR=:18080 MAILDEV_HTTPS_ADDR=:18443 MAILDEV_SMTP_ADDRS=:11025 MAILDEV_SMTPS_ADDRS=:11465 MAILDEV_IMAP_ADDRS=:11143 MAILDEV_IMAPS_ADDRS=:11993 MAILDEV_POP3_ADDRS=:11110 MAILDEV_POP3S_ADDRS=:11995 MAILDEV_SMIME_SYSTEM_ROOTS=false"

echo "== full-edition suites (dev auth, multi-project)"
env $common MAILDEV_DATA_DIR="$tmp/full" MAILDEV_EDITION=full MAILDEV_AUTH_MODE=dev MAILDEV_DEMO_PROJECT=true MAILDEV_ADMIN_USERS=admin "$tmp/maildev-lite" > "$tmp/full.log" 2>&1 &
pid=$!
for i in $(seq 1 50); do curl -fs localhost:18080/readyz >/dev/null 2>&1 && break; sleep 0.2; done
python3 -I "$backend/scripts/smoke_test.py"
python3 -I "$backend/scripts/relay_test.py"
python3 -I "$backend/scripts/local_users_test.py"
kill $pid; wait $pid 2>/dev/null || true

echo "== lite specifics (local auth, single project)"
env $common MAILDEV_DATA_DIR="$tmp/lite" "$tmp/maildev-lite" > "$tmp/lite.log" 2>&1 &
pid=$!
for i in $(seq 1 50); do curl -fs localhost:18080/readyz >/dev/null 2>&1 && break; sleep 0.2; done
python3 -I "$here/scripts/lite_test.py"
