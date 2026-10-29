#!/usr/bin/env bash
# One-time OpenBao setup for MailDev per-project email encryption.
#   - enables the Transit engine at transit/ (if missing)
#   - creates policy maildev-transit scoped to keys named maildev-*
#   - creates AppRole "maildev" and prints role_id / secret_id for the k8s secret
# Requires BAO_ADDR and a BAO_TOKEN with sufficient rights (e.g. from /root/.bashrc).
set -euo pipefail
: "${BAO_ADDR:=https://vault.example.com}"
: "${BAO_TOKEN:?BAO_TOKEN is required}"
H=(-H "X-Vault-Token: $BAO_TOKEN" -H "Content-Type: application/json")
api() { curl -sf "${H[@]}" -X "$1" "$BAO_ADDR/v1/$2" ${3:+-d "$3"}; }

if ! api GET sys/mounts | python3 -c "import json,sys; sys.exit(0 if 'transit/' in json.load(sys.stdin)['data'] else 1)"; then
  api POST sys/mounts/transit '{"type":"transit","description":"MailDev per-project email encryption keys"}' >/dev/null
  echo "enabled transit/"
else
  echo "transit/ already enabled"
fi

POLICY='path "transit/keys/maildev-*" { capabilities = ["create","read","update","delete"] }
path "transit/encrypt/maildev-*" { capabilities = ["update"] }
path "transit/decrypt/maildev-*" { capabilities = ["update"] }
path "auth/token/renew-self" { capabilities = ["update"] }
path "auth/token/lookup-self" { capabilities = ["read"] }'
api PUT sys/policies/acl/maildev-transit "$(python3 -c "import json,sys; print(json.dumps({'policy': sys.stdin.read()}))" <<<"$POLICY")" >/dev/null
echo "policy maildev-transit written"

if ! api GET sys/auth | python3 -c "import json,sys; sys.exit(0 if 'approle/' in json.load(sys.stdin)['data'] else 1)"; then
  api POST sys/auth/approle '{"type":"approle"}' >/dev/null
  echo "enabled approle auth"
fi
api POST auth/approle/role/maildev '{"token_policies":["maildev-transit"],"token_ttl":"1h","token_max_ttl":"24h","secret_id_num_uses":0,"secret_id_ttl":0}' >/dev/null
ROLE_ID=$(api GET auth/approle/role/maildev/role-id | python3 -c "import json,sys; print(json.load(sys.stdin)['data']['role_id'])")
SECRET_ID=$(api POST auth/approle/role/maildev/secret-id '{}' | python3 -c "import json,sys; print(json.load(sys.stdin)['data']['secret_id'])")
echo "approle maildev ready"

NS=${NS:-maildev}
if [ "${APPLY_K8S:-1}" = "1" ]; then
  kubectl -n "$NS" create secret generic maildev-openbao \
    --from-literal=MAILDEV_OPENBAO_ROLE_ID="$ROLE_ID" \
    --from-literal=MAILDEV_OPENBAO_SECRET_ID="$SECRET_ID" \
    --dry-run=client -o yaml | kubectl apply -f -
  echo "k8s secret maildev-openbao applied in namespace $NS"
else
  echo "MAILDEV_OPENBAO_ROLE_ID=$ROLE_ID"
  echo "MAILDEV_OPENBAO_SECRET_ID=$SECRET_ID"
fi
