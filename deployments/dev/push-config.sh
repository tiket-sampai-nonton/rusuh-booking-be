#!/usr/bin/env bash
# Dijalankan di LAPTOP (folder rusuh-booking-be). Mengirim file konfigurasi deploy ke server TANPA lewat GitHub Actions,
# supaya server bisa disiapkan dan diperiksa (gen-secrets.sh, preflight.sh) sebelum deploy pertama. Isinya sama persis
# dengan yang dikirim job deployment-dev.
#   bash deployments/dev/push-config.sh                                   # bawaan: deploy@103.119.237.53 port 20130
#   SSH_TARGET=deploy@103.119.237.53 SSH_PORT=20130 bash deployments/dev/push-config.sh
set -euo pipefail
cd "$(dirname "$0")/../.."
# IP langsung, bukan nama domain: dev.rusuhbooking.my.id di Cloudflare berstatus Proxied dan tidak meneruskan SSH.
SSH_TARGET="${SSH_TARGET:-deploy@103.119.237.53}"
SSH_PORT="${SSH_PORT:-20130}"
SSH_KEY_OPT=()
[ -n "${SSH_KEY:-}" ] && SSH_KEY_OPT=(-i "$SSH_KEY")

REMOTE_SYNC=$(cat <<'EOS'
set -eu
# Sinkronkan konfigurasi dari repo ke server TANPA meninggalkan berkas lama: berkas di deployments/observability yang sudah
# dihapus atau di-rename di repo ikut dihapus (dashboard dan alert lama bisa bentrok, uid sama). Yang dihapus hanya BERKAS,
# bukan folder: Grafana memasang folder itu sebagai bind mount, dan folder yang dihapus lalu dibuat ulang tidak terlihat
# container sampai di-recreate. cp menimpa berkas di tempat, jadi inode folder dan berkas yang ada tidak berganti.
R="${RUSUH_REPO:-/opt/rusuh/repo}"
T="$(mktemp -d)"
trap 'rm -rf "$T"' EXIT
mkdir -p "$R"
tar -C "$T" -xzf -
if [ -d "$R/deployments/observability" ]; then
  (cd "$R" && find deployments/observability -type f) | while IFS= read -r f; do
    [ -e "$T/$f" ] || rm -v "$R/$f"
  done
fi
cp -a "$T"/. "$R"/
EOS
)

[ -f deployments/dev/docker-compose.dev.yaml ] || { echo "jalankan dari repo rusuh-booking-be" >&2; exit 1; }
COPYFILE_DISABLE=1 tar -czf - deployments/dev deployments/compose/postgres-init.sql deployments/observability \
  | ssh -p "$SSH_PORT" ${SSH_KEY_OPT[@]+"${SSH_KEY_OPT[@]}"} "$SSH_TARGET" "$REMOTE_SYNC && chmod +x /opt/rusuh/repo/deployments/dev/*.sh"
echo "terkirim ke $SSH_TARGET:/opt/rusuh/repo"
ssh -p "$SSH_PORT" ${SSH_KEY_OPT[@]+"${SSH_KEY_OPT[@]}"} "$SSH_TARGET" 'ls /opt/rusuh/repo/deployments/dev | head -30'
