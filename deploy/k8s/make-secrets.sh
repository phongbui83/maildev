#!/usr/bin/env bash
# Creates the two secrets that are intentionally not committed:
#   maildev-secrets : database URL and the credential master key
#   ad-ca           : pinned certificate of the AD LDAPS endpoint (fetched with openssl)
# Safe to re-run: existing values are kept unless FORCE=1.
#
# Usage (external PostgreSQL, the default setup):
#   DATABASE_URL='postgres://maildev:<password>@db.example.com:5432/maildev?sslmode=require' bash deploy/k8s/make-secrets.sh
# With the optional in-cluster postgres.optional.yaml, omit DATABASE_URL and a
# password is generated (also stored as POSTGRES_PASSWORD for the StatefulSet).
set -euo pipefail
NS=${NS:-maildev}
LDAP_HOST=${LDAP_HOST:-dc01.example.com}
LDAP_PORT=${LDAP_PORT:-636}

kubectl get ns "$NS" >/dev/null 2>&1 || kubectl create ns "$NS"

if ! kubectl -n "$NS" get secret maildev-secrets >/dev/null 2>&1 || [ "${FORCE:-0}" = "1" ]; then
  MASTER_KEY=${MASTER_KEY:-$(openssl rand -base64 32)}
  if [ -n "${DATABASE_URL:-}" ]; then
    kubectl -n "$NS" create secret generic maildev-secrets \
      --from-literal=MAILDEV_MASTER_KEY="$MASTER_KEY" \
      --from-literal=MAILDEV_DATABASE_URL="$DATABASE_URL" \
      --dry-run=client -o yaml | kubectl apply -f -
  else
    PG_PASS=$(openssl rand -base64 24 | tr -d '/+=' | cut -c1-32)
    kubectl -n "$NS" create secret generic maildev-secrets \
      --from-literal=POSTGRES_PASSWORD="$PG_PASS" \
      --from-literal=MAILDEV_MASTER_KEY="$MASTER_KEY" \
      --from-literal=MAILDEV_DATABASE_URL="postgres://maildev:${PG_PASS}@postgres.${NS}.svc:5432/maildev?sslmode=disable" \
      --dry-run=client -o yaml | kubectl apply -f -
  fi
  echo "created maildev-secrets (keep MAILDEV_MASTER_KEY safe: stored credentials cannot be decrypted without it)"
else
  echo "maildev-secrets already exists, keeping it (FORCE=1 to regenerate)"
fi

# Pin the LDAPS certificate. If you have the AD CA root certificate, pass it as
# AD_CA_FILE=path instead; a CA file keeps working across AD certificate renewals.
if [ -n "${AD_CA_FILE:-}" ]; then
  CA_SRC="$AD_CA_FILE"
else
  CA_SRC=$(mktemp)
  openssl s_client -connect "${LDAP_HOST}:${LDAP_PORT}" -servername "$LDAP_HOST" </dev/null 2>/dev/null | openssl x509 -out "$CA_SRC"
  echo "pinned leaf certificate of ${LDAP_HOST}:"
  openssl x509 -in "$CA_SRC" -noout -subject -enddate
  echo "NOTE: re-run this script when the AD certificate is renewed (or supply AD_CA_FILE with the AD CA root)."
fi
kubectl -n "$NS" create secret generic ad-ca --from-file=ca.crt="$CA_SRC" --dry-run=client -o yaml | kubectl apply -f -
