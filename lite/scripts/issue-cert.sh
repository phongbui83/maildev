#!/usr/bin/env bash
# Issue the TLS certificate of a lite site from the OpenBao PKI
# (pki-int, role tls-any, 10 years) and fetch the CA chain for S/MIME trust.
#
#   BAO_ADDR=https://vault.example.com BAO_TOKEN=... scripts/issue-cert.sh <hostname> [ip] [outdir]
#
# Output: <outdir>/tls.crt (leaf + chain), <outdir>/tls.key, <outdir>/smime-ca.pem, <outdir>/ca.crt
set -euo pipefail
host=${1:?hostname}
ip=${2:-}
out=${3:-certs}
: "${BAO_ADDR:=https://vault.example.com}"
: "${BAO_TOKEN:?BAO_TOKEN is required}"
mount=${BAO_PKI_MOUNT:-pki-int}
role=${BAO_PKI_ROLE:-tls-any}
ttl=${CERT_TTL:-87600h}

mkdir -p "$out"
alt="$host,localhost"
body=$(python3 - "$host" "$alt" "$ip" "$ttl" <<'PY'
import json,sys
host,alt,ip,ttl=sys.argv[1:5]
d={"common_name":host,"alt_names":alt,"ttl":ttl,"format":"pem"}
ips=["127.0.0.1"]
if ip: ips.insert(0,ip)
d["ip_sans"]=",".join(ips)
print(json.dumps(d))
PY
)
resp=$(curl -fsS -H "X-Vault-Token: $BAO_TOKEN" -X POST "$BAO_ADDR/v1/$mount/issue/$role" -d "$body")
python3 - "$out" <<'PY' "$resp"
import json,sys,os
out=sys.argv[1]; d=json.loads(sys.argv[2])["data"]
chain="\n".join(d.get("ca_chain") or [d["issuing_ca"]])
open(os.path.join(out,"tls.crt"),"w").write(d["certificate"].strip()+"\n"+chain.strip()+"\n")
open(os.path.join(out,"tls.key"),"w").write(d["private_key"].strip()+"\n")
open(os.path.join(out,"ca.crt"),"w").write(chain.strip()+"\n")
os.chmod(os.path.join(out,"tls.key"),0o600)
print("issued", d.get("serial_number"), "expires", d.get("expiration"))
PY
# Trust anchors for S/MIME signature verification (intermediate + root).
curl -fsS "$BAO_ADDR/v1/$mount/ca_chain" > "$out/smime-ca.pem"
echo "wrote $out/tls.crt $out/tls.key $out/ca.crt $out/smime-ca.pem"
openssl x509 -in "$out/tls.crt" -noout -subject -enddate -ext subjectAltName 2>/dev/null | sed 's/^/  /'
