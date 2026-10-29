#!/usr/bin/env bash
# Build the air-gap bundle for one customer site:
#   dist/maildev-lite-<version>-<host>.tar.gz containing the image (docker save),
#   docker-compose.yml, .env, certs/ and the install guide.
#
#   BAO_TOKEN=... scripts/package.sh <hostname> [ip]
set -euo pipefail
host=${1:?hostname}
ip=${2:-}
here=$(cd "$(dirname "$0")/.." && pwd)
version=${VERSION:-$(date +%Y%m%d%H%M)}
image=${IMAGE:-ghcr.io/phongbui83/maildev/lite}
stage=$here/dist/bundle-$host
rm -rf "$stage" && mkdir -p "$stage/certs" "$stage/data" "$here/dist"

(cd "$here/.." && docker build -f lite/Dockerfile --build-arg VERSION="$version" -t "$image:$version" -t "$image:latest" .)
"$here/scripts/issue-cert.sh" "$host" "$ip" "$stage/certs"
docker save "$image:$version" | gzip > "$stage/maildev-lite-image-$version.tar.gz"
cp "$here/docker-compose.yml" "$stage/"
sed -e "s#^MAILDEV_HOSTNAME=.*#MAILDEV_HOSTNAME=$host#" -e "s#^MAILDEV_PUBLIC_URL=.*#MAILDEV_PUBLIC_URL=https://$host#" "$here/.env.example" > "$stage/.env"
echo "MAILDEV_IMAGE=$image:$version" >> "$stage/.env"
cp "$here/README.md" "$stage/README.md"
cat > "$stage/install.sh" <<'SH'
#!/usr/bin/env bash
# Run on the customer host (Docker + docker compose plugin required, no internet needed).
set -euo pipefail
cd "$(dirname "$0")"
docker load < maildev-lite-image-*.tar.gz
mkdir -p data && chmod 700 data
docker compose up -d
echo "MailDev lite is starting. Open https://$(grep ^MAILDEV_HOSTNAME= .env | cut -d= -f2)/ and sign in as admin / admin."
SH
chmod +x "$stage/install.sh"
tar -C "$here/dist" -czf "$here/dist/maildev-lite-$version-$host.tar.gz" "bundle-$host"
echo "bundle: $here/dist/maildev-lite-$version-$host.tar.gz"
du -sh "$here/dist/maildev-lite-$version-$host.tar.gz"
