#!/usr/bin/env bash
# Dijalankan di LAPTOP (folder rusuh-booking-be). Mengirim file konfigurasi deploy ke server TANPA lewat GitHub Actions,
# supaya server bisa disiapkan dan diperiksa (gen-secrets.sh, preflight.sh) sebelum deploy pertama. Isinya sama persis
# dengan yang dikirim job deployment-dev.
#   bash deployments/dev/push-config.sh                                   # bawaan: deploy@dev.rusuhbooking.my.id port 20130
#   SSH_TARGET=deploy@103.119.237.53 SSH_PORT=20130 bash deployments/dev/push-config.sh
set -euo pipefail
cd "$(dirname "$0")/../.."
SSH_TARGET="${SSH_TARGET:-deploy@dev.rusuhbooking.my.id}"
SSH_PORT="${SSH_PORT:-20130}"
SSH_KEY_OPT=()
[ -n "${SSH_KEY:-}" ] && SSH_KEY_OPT=(-i "$SSH_KEY")

[ -f deployments/dev/docker-compose.dev.yaml ] || { echo "jalankan dari repo rusuh-booking-be" >&2; exit 1; }
tar -czf - deployments/dev deployments/compose/postgres-init.sql deployments/observability \
  | ssh -p "$SSH_PORT" "${SSH_KEY_OPT[@]}" "$SSH_TARGET" 'mkdir -p /opt/rusuh/repo && tar -C /opt/rusuh/repo -xzf - && chmod +x /opt/rusuh/repo/deployments/dev/*.sh'
echo "terkirim ke $SSH_TARGET:/opt/rusuh/repo"
ssh -p "$SSH_PORT" "${SSH_KEY_OPT[@]}" "$SSH_TARGET" 'ls /opt/rusuh/repo/deployments/dev | head -30'
