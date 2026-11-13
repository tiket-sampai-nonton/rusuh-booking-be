#!/usr/bin/env bash
# Dijalankan di LAPTOP. Membuka SSH tunnel ke layanan dev yang hanya mendengarkan di loopback server (tidak ke internet):
#   localhost:18082 -> booking-api (API admin: /internal/v1/admin/*, termasuk unggah gambar)
#   localhost:18081 -> queue-api   (PUT /internal/v1/admin/events/{id}/queue-settings; dipakai createEvent di tests/k6).
#     queue-api tidak dipublikasikan di loopback VPS, jadi IP kontainernya dicari saat tunnel dibuka; bila queue-api dibuat ulang,
#     IP berubah dan tunnel harus dibuka ulang.
#   localhost:15432 -> Postgres    localhost:16379 -> Valkey
# Port lokal sengaja tinggi agar tidak bentrok dengan Postgres/Valkey/booking lokal. Tekan Ctrl+C untuk menutup.
#   bash deployments/dev/dev-tunnel.sh                          # bawaan: deploy@103.119.237.53 port 20130
#   SSH_TARGET=rusuh-dev-deploy bash deployments/dev/dev-tunnel.sh
set -euo pipefail
SSH_TARGET="${SSH_TARGET:-deploy@103.119.237.53}"
SSH_PORT="${SSH_PORT:-20130}"
SSH_KEY_OPT=()
[ -n "${SSH_KEY:-}" ] && SSH_KEY_OPT=(-i "$SSH_KEY")

SSH_OPTS=(-p "$SSH_PORT" ${SSH_KEY_OPT[@]+"${SSH_KEY_OPT[@]}"})
QUEUE_IP="$(ssh "${SSH_OPTS[@]}" -o BatchMode=yes -o ConnectTimeout=10 "$SSH_TARGET" \
  "docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' rusuh-dev-queue-api-1" 2>/dev/null || true)"
QUEUE_FWD=()
if [ -n "$QUEUE_IP" ]; then QUEUE_FWD=(-L "127.0.0.1:18081:$QUEUE_IP:8081"); else echo "peringatan: IP queue-api tidak ditemukan; admin queue (18081) tidak diteruskan" >&2; fi

echo "tunnel ke $SSH_TARGET: admin http://localhost:18082 | queue-admin http://localhost:18081 | postgres localhost:15432 | valkey localhost:16379 (Ctrl+C untuk berhenti)"
exec ssh -N -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -p "$SSH_PORT" ${SSH_KEY_OPT[@]+"${SSH_KEY_OPT[@]}"} \
  ${QUEUE_FWD[@]+"${QUEUE_FWD[@]}"} -L 127.0.0.1:18082:127.0.0.1:8082 -L 127.0.0.1:15432:127.0.0.1:5432 -L 127.0.0.1:16379:127.0.0.1:6379 "$SSH_TARGET"
