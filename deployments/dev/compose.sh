#!/usr/bin/env bash
# Pembungkus `docker compose` untuk dev di server: memuat IMAGE_PREFIX/IMAGE_TAG dari deploy terakhir dan variabel
# lain yang dibutuhkan Compose, sehingga Anda cukup mengetik subperintah biasa:
#   ./compose.sh ps
#   ./compose.sh logs -f --tail 100 booking-api
#   ./compose.sh restart notification-worker
#   ./compose.sh up -d --force-recreate booking-api booking-worker     # setelah mengubah /opt/rusuh/env/booking.env
set -euo pipefail
RUSUH_DIR="${RUSUH_DIR:-/opt/rusuh}"
STATE="$RUSUH_DIR/.deploy-state"
if [ -f "$STATE" ]; then set -a; . "$STATE"; set +a; fi
export IMAGE_PREFIX="${IMAGE_PREFIX:-ghcr.io/belum/deploy}" IMAGE_TAG="${IMAGE_TAG:-belum}"
export RUSUH_DIR RUSUH_UID="$(id -u)" RUSUH_GID="$(id -g)"
cd "$(dirname "$0")"
exec docker compose -f docker-compose.dev.yaml "$@"
