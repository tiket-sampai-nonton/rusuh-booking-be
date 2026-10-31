#!/usr/bin/env bash
# Pemeriksaan sebelum deploy pertama (dan kapan pun ada keraguan). Dijalankan di server sebagai user deploy:
#   bash /opt/rusuh/repo/deployments/dev/preflight.sh
# Tidak mengubah apa pun dan tidak menampilkan nilai rahasia. Exit 1 bila ada [GAGAL].
set -uo pipefail
RUSUH_DIR="${RUSUH_DIR:-/opt/rusuh}"
HERE="$(cd "$(dirname "$0")" && pwd)"
fail=0
ok()   { echo "  [OK]    $*"; }
bad()  { echo "  [GAGAL] $*"; fail=1; }
info() { echo "  [CEK]   $*"; }
val()  { grep -E "^$2=" "$RUSUH_DIR/env/$1.env" 2>/dev/null | head -1 | cut -d= -f2-; }

echo "1. Docker"
if command -v docker >/dev/null 2>&1 && docker info >/dev/null 2>&1; then ok "docker aktif ($(docker --version | cut -d, -f1))"; else bad "docker tidak bisa dipakai user $(id -un) (grup docker? login ulang SSH)"; fi
docker compose version >/dev/null 2>&1 && ok "docker compose tersedia" || bad "docker compose tidak ada"

echo "2. Kunci token"
for k in user queue payment booking; do
  [ -f "$RUSUH_DIR/keys/$k-token.pem" ] && [ -f "$RUSUH_DIR/keys/$k-token.pub.pem" ] && ok "kunci $k" || bad "kunci $k belum ada (jalankan gen-secrets.sh)"
done

echo "3. File env"
for f in compose postgres rabbitmq valkey grafana queue booking payment notification user; do
  if [ ! -f "$RUSUH_DIR/env/$f.env" ]; then bad "$f.env belum ada (jalankan gen-secrets.sh)"; continue; fi
  perm="$(stat -c %a "$RUSUH_DIR/env/$f.env" 2>/dev/null || echo ?)"
  if grep -qE '@@[A-Z_]+@@' "$RUSUH_DIR/env/$f.env"; then
    bad "$f.env masih punya nilai kosong: $(grep -oE '@@[A-Z_]+@@' "$RUSUH_DIR/env/$f.env" | sort -u | tr '\n' ' ')"
  elif [ "$perm" != "600" ]; then bad "$f.env izin $perm (harus 600: chmod 600 $RUSUH_DIR/env/$f.env)"
  else ok "$f.env lengkap (izin 600)"; fi
done

echo "4. Format kunci dari luar"
mk="$(val payment MIDTRANS_SERVER_KEY)"; case "$mk" in SB-Mid-server-*) ok "MIDTRANS_SERVER_KEY berformat Sandbox";; "") bad "MIDTRANS_SERVER_KEY kosong";; *) info "MIDTRANS_SERVER_KEY tidak berawalan SB-Mid-server- (bukan kunci Sandbox? pastikan MIDTRANS_ENVIRONMENT sesuai)";; esac
rk="$(val notification RESEND_API_KEY)"; case "$rk" in re_*) ok "RESEND_API_KEY berformat re_...";; "") bad "RESEND_API_KEY kosong";; *) info "RESEND_API_KEY tidak berawalan re_";; esac
ae="$(val booking ADMIN_EMAILS)"; [ -n "$ae" ] && ok "ADMIN_EMAILS terisi" || info "ADMIN_EMAILS kosong: tidak ada yang bisa memakai endpoint admin"
allow="$(val compose ADMIN_ALLOW_IPS)"; [ "$allow" = "127.0.0.1" ] && info "ADMIN_ALLOW_IPS=127.0.0.1: endpoint admin tertutup dari internet (ubah bila perlu, lihat panduan)"

echo "5. Domain dan port"
domain="$(val compose DEV_DOMAIN)"
if [ -n "$domain" ]; then
  ip="$(getent ahostsv4 "$domain" 2>/dev/null | awk 'NR==1{print $1}')"
  [ -n "$ip" ] && ok "$domain -> $ip" || bad "$domain belum resolve (record A di DNS Domainesia?)"
  g="$(getent ahostsv4 "grafana.$domain" 2>/dev/null | awk 'NR==1{print $1}')"
  [ -n "$g" ] && ok "grafana.$domain -> $g" || bad "grafana.$domain belum resolve (tambahkan record A grafana.dev)"
fi
if ss -tln 2>/dev/null | grep -qE ':(80|443)\s'; then
  if docker ps --format '{{.Names}}' 2>/dev/null | grep -q caddy; then ok "80/443 dipakai Caddy kita"; else bad "port 80/443 dipakai proses lain: $(ss -tlnp 2>/dev/null | grep -E ':(80|443)\s' | head -2)"; fi
else ok "port 80 dan 443 kosong"; fi

echo "6. Sumber daya"
avail="$(df --output=avail -BG / | tail -1 | tr -dc '0-9')"; [ "${avail:-0}" -ge 10 ] && ok "disk kosong ${avail} GB" || bad "disk kosong hanya ${avail} GB"
swapon --show 2>/dev/null | grep -q . && ok "swap aktif" || info "swap tidak aktif"
mem="$(awk '/MemAvailable/{printf "%d", $2/1024}' /proc/meminfo)"; [ "${mem:-0}" -ge 3000 ] && ok "memori tersedia ${mem} MB" || info "memori tersedia hanya ${mem} MB"

echo "7. Compose valid"
if IMAGE_PREFIX=ghcr.io/x/y IMAGE_TAG=x RUSUH_DIR="$RUSUH_DIR" RUSUH_UID="$(id -u)" RUSUH_GID="$(id -g)" docker compose -f "$HERE/docker-compose.dev.yaml" config -q 2>/tmp/preflight-compose.err; then ok "docker-compose.dev.yaml valid"; else bad "compose tidak valid: $(head -3 /tmp/preflight-compose.err)"; fi

echo
if [ "$fail" = 0 ]; then echo "SIAP DEPLOY. Jalankan workflow ci (deploy_env=dev)."; else echo "BELUM SIAP: perbaiki [GAGAL] di atas lalu jalankan ulang."; exit 1; fi
