#!/usr/bin/env bash
# Deploy dev di server (dipanggil job deployment-dev lewat SSH, atau manual oleh user deploy).
#   IMAGE_PREFIX=ghcr.io/tiket-sampai-nonton/rusuh-booking-be IMAGE_TAG=<sha> bash deploy.sh
# Migrasi database selalu jalan (idempoten, dikunci advisory lock) karena service bergantung padanya di Compose.
set -euo pipefail

: "${IMAGE_PREFIX:?IMAGE_PREFIX wajib, mis. ghcr.io/<owner>/<repo>}"
: "${IMAGE_TAG:?IMAGE_TAG wajib (sha commit)}"
export IMAGE_PREFIX IMAGE_TAG
export RUSUH_DIR="${RUSUH_DIR:-/opt/rusuh}"
export RUSUH_UID="$(id -u)" RUSUH_GID="$(id -g)"
cd "$(dirname "$0")"
dc() { docker compose -f docker-compose.dev.yaml "$@"; }

# 1. konfigurasi harus lengkap
for f in compose postgres rabbitmq valkey grafana queue booking payment notification user; do
  [ -f "$RUSUH_DIR/env/$f.env" ] || { echo "::error::$RUSUH_DIR/env/$f.env belum ada: jalankan gen-secrets.sh" >&2; exit 1; }
done
if grep -lE '@@[A-Z_]+@@' "$RUSUH_DIR"/env/*.env >/dev/null 2>&1; then
  echo "::error::masih ada nilai @@...@@ yang belum diisi di $RUSUH_DIR/env (jalankan gen-secrets.sh untuk daftarnya)" >&2; exit 1
fi
for k in user queue payment booking; do
  [ -f "$RUSUH_DIR/keys/$k-token.pem" ] || { echo "::error::kunci $k tidak ada" >&2; exit 1; }
done
mkdir -p "$RUSUH_DIR/data/images"

# 2. tarik image lalu jalankan (migrasi ikut otomatis lewat depends_on)
dc pull
dc up -d --remove-orphans

# 3. smoke test lewat Caddy: katalog event harus 200 (menunggu semua service naik)
domain="$(grep -E '^DEV_DOMAIN=' "$RUSUH_DIR/env/compose.env" | cut -d= -f2-)"
for i in $(seq 1 36); do
  code="$(curl -ksS -o /dev/null -w '%{http_code}' --resolve "$domain:443:127.0.0.1" "https://$domain/api/v1/events" || true)"
  if [ "$code" = "200" ]; then echo "smoke test lulus (GET /api/v1/events = 200)"; break; fi
  echo "menunggu service ($i/36), status=$code"; sleep 5
  if [ "$i" = 36 ]; then echo "::error::smoke test gagal; lihat: docker compose -f $PWD/docker-compose.dev.yaml logs --tail 50" >&2; dc ps; exit 1; fi
done

dc ps --format 'table {{.Service}}\t{{.Status}}'
echo "deploy selesai: $IMAGE_PREFIX:*:$IMAGE_TAG"
