#!/usr/bin/env bash
# Deploy dev di server (dipanggil job deployment-dev lewat SSH, atau manual oleh user deploy).
#   IMAGE_PREFIX=ghcr.io/tiket-sampai-nonton/rusuh-booking-be IMAGE_TAG=<sha> bash deploy.sh
# Opsional TAG_QUEUE|TAG_BOOKING|TAG_PAYMENT|TAG_NOTIFICATION|TAG_USER=<sha> menimpa IMAGE_TAG untuk satu service
# (CI: service yang berubah memakai sha PR, sisanya IMAGE_TAG=main).
# Migrasi database selalu jalan (idempoten, dikunci advisory lock) karena service bergantung padanya di Compose.
set -euo pipefail

: "${IMAGE_PREFIX:?IMAGE_PREFIX wajib, mis. ghcr.io/<owner>/<repo>}"
: "${IMAGE_TAG:?IMAGE_TAG wajib (sha commit)}"
export IMAGE_PREFIX IMAGE_TAG
export TAG_QUEUE="${TAG_QUEUE:-}" TAG_BOOKING="${TAG_BOOKING:-}" TAG_PAYMENT="${TAG_PAYMENT:-}" TAG_NOTIFICATION="${TAG_NOTIFICATION:-}" TAG_USER="${TAG_USER:-}"
export RUSUH_DIR="${RUSUH_DIR:-/opt/rusuh}"
RUSUH_UID="$(id -u)"
RUSUH_GID="$(id -g)"
export RUSUH_UID RUSUH_GID
cd "$(dirname "$0")"
dc() { docker compose -f docker-compose.dev.yaml "$@"; }

# 1. konfigurasi harus lengkap
for f in compose postgres rabbitmq valkey grafana queue booking payment notification user; do
  [ -f "$RUSUH_DIR/env/$f.env" ] || { echo "::error::$RUSUH_DIR/env/$f.env belum ada: jalankan gen-secrets.sh" >&2; exit 1; }
done
if grep -lE '@@[A-Z_]+@@' "$RUSUH_DIR"/env/*.env >/dev/null 2>&1; then
  echo "::error::masih ada nilai @@...@@ yang belum diisi di $RUSUH_DIR/env (jalankan gen-secrets.sh untuk daftarnya)" >&2; exit 1
fi
if ! missing="$(bash ./check-env.sh)"; then
  echo "::error::env di server tertinggal dari template (gen-secrets.sh tidak menimpa file lama):" >&2
  echo "$missing" >&2; exit 1
fi
for k in user queue payment booking; do
  [ -f "$RUSUH_DIR/keys/$k-token.pem" ] || { echo "::error::kunci $k tidak ada" >&2; exit 1; }
done
mkdir -p "$RUSUH_DIR/data/images"

# 2. tarik image lalu jalankan (migrasi ikut otomatis lewat depends_on)
dc pull
dc up -d --remove-orphans

# 2b. `up -d` tidak membuat ulang container bila hanya isi file konfigurasi yang di-bind-mount berubah, dan file tunggal
#     yang diganti tar tetap terbaca versi lamanya. Restart hanya service yang konfigurasinya berubah sejak deploy lalu.
#     Dashboard Grafana tidak perlu: folder dashboard dibaca ulang otomatis.
mkdir -p "$RUSUH_DIR/.config-sums"
for pair in caddy:Caddyfile prometheus:prometheus.dev.yml loki:loki.dev.yaml alloy:alloy.dev.alloy \
            grafana:../observability/grafana/provisioning; do
  svc="${pair%%:*}" path="${pair#*:}"
  sum="$(find "$path" -type f -print0 | sort -z | xargs -0 sha256sum | sha256sum | cut -d' ' -f1)"
  if [ "$(cat "$RUSUH_DIR/.config-sums/$svc" 2>/dev/null)" != "$sum" ]; then
    echo "konfigurasi $svc berubah: restart"
    dc restart "$svc"
    echo "$sum" > "$RUSUH_DIR/.config-sums/$svc"
  fi
done

# 3. smoke test: setiap container yang punya HEALTHCHECK (semua api dan worker) harus healthy, dan katalog lewat Caddy
#    harus 200. Katalog saja tidak cukup: ia dilayani Booking, jadi queue-api atau worker yang mati tetap lolos.
domain="$(grep -E '^DEV_DOMAIN=' "$RUSUH_DIR/env/compose.env" | cut -d= -f2-)"
for i in $(seq 1 36); do
  notready="$(dc ps --format '{{.Service}} {{.Health}}' | awk '$2 != "" && $2 != "healthy" {print $1}' | tr '\n' ' ')"
  code="$(curl -ksS -o /dev/null -w '%{http_code}' --resolve "$domain:443:127.0.0.1" "https://$domain/api/v1/events" || true)"
  if [ -z "$notready" ] && [ "$code" = "200" ]; then echo "smoke test lulus (semua container healthy, GET /api/v1/events = 200)"; break; fi
  echo "menunggu service ($i/36), katalog=$code, belum healthy: ${notready:-tidak ada}"; sleep 5
  if [ "$i" = 36 ]; then echo "::error::smoke test gagal; lihat: docker compose -f $PWD/docker-compose.dev.yaml logs --tail 50" >&2; dc ps; exit 1; fi
done

# simpan versi yang sedang berjalan agar compose.sh memakainya
printf 'IMAGE_PREFIX=%s\nIMAGE_TAG=%s\nTAG_QUEUE=%s\nTAG_BOOKING=%s\nTAG_PAYMENT=%s\nTAG_NOTIFICATION=%s\nTAG_USER=%s\n' \
  "$IMAGE_PREFIX" "$IMAGE_TAG" "$TAG_QUEUE" "$TAG_BOOKING" "$TAG_PAYMENT" "$TAG_NOTIFICATION" "$TAG_USER" > "$RUSUH_DIR/.deploy-state"

dc ps --format 'table {{.Service}}\t{{.Status}}'
echo "deploy selesai: $IMAGE_PREFIX:*:$IMAGE_TAG"
