#!/usr/bin/env bash
# Refresh the backend packages copied into the lite edition (same module path;
# only the PostgreSQL store and migration runner are replaced by internal/store,
# SQLite). The frontend is built straight from ../frontend. Run after changing
# backend/internal.
#
#   scripts/sync-from-full.sh [../backend]
set -euo pipefail
here=$(cd "$(dirname "$0")/.." && pwd)
backend=$(cd "${1:-$here/../backend}" && pwd)

# Packages owned by the full edition. Everything under internal/ except the
# lite-specific ones (store, webui) is a copy.
keep=(store webui)
for d in "$here"/internal/*/; do
  name=$(basename "$d")
  skip=false
  for k in "${keep[@]}"; do [[ "$name" == "$k" ]] && skip=true; done
  $skip || rm -rf "$d"
done
for d in "$backend"/internal/*/; do
  name=$(basename "$d")
  case "$name" in store|db) continue ;; esac
  rsync -a --delete --exclude '*_test.go' "$d" "$here/internal/$name/"
done

echo "synced backend internal/ (except store, db) from:"
echo "  $backend ($(git -C "$backend" rev-parse --short HEAD))"

