#!/usr/bin/env bash
# Nyalakan stack lokal untuk uji beban: compose repo + override rate limit (env/local.compose.yaml).
#   bash tests/k6/scripts/stack-local.sh up|down|ps
# MIDTRANS_SERVER_KEY diambil dari services/payment/.env bila belum ada di environment (nilai tidak dicetak).
set -euo pipefail
cd "$(dirname "$0")/../../.."
if [ -z "${MIDTRANS_SERVER_KEY:-}" ] && [ -f services/payment/.env ]; then
  MIDTRANS_SERVER_KEY="$(grep -m1 '^MIDTRANS_SERVER_KEY=' services/payment/.env | cut -d= -f2-)"; export MIDTRANS_SERVER_KEY
fi
dc() { docker compose -f docker-compose.yaml -f tests/k6/env/local.compose.yaml "$@"; }
case "${1:-up}" in
  up) make keys >/dev/null; dc up -d --build ;;
  down) dc stop ;;
  ps) dc ps --format '{{.Service}} {{.Status}}' ;;
esac
