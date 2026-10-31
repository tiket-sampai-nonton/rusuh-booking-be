#!/usr/bin/env bash
# Membuat /opt/rusuh/{env,keys,data,backups} di server dev: kunci Ed25519, rahasia acak, dan file env dari env/*.env.example.
# Aman dijalankan ulang: file yang sudah ada TIDAK ditimpa (rahasia tidak berubah).
#
#   DEV_DOMAIN=dev.rusuhbooking.my.id ACME_EMAIL=anda@contoh.id \
#   ADMIN_EMAILS=anda@contoh.id MIDTRANS_SERVER_KEY=... RESEND_API_KEY=... \
#   bash gen-secrets.sh
#
# Nilai rahasia luar (Midtrans, Resend) diberikan lewat variabel lingkungan sesaat itu saja, jangan lewat chat atau repo.
# Token @@SET_...@@ yang tidak diberikan dibiarkan; deploy.sh menolak jalan sebelum semuanya terisi.
set -euo pipefail

RUSUH_DIR="${RUSUH_DIR:-/opt/rusuh}"
HERE="$(cd "$(dirname "$0")" && pwd)"
mkdir -p "$RUSUH_DIR"/{env,keys,data/images,backups}
chmod 700 "$RUSUH_DIR" "$RUSUH_DIR"/env "$RUSUH_DIR"/keys "$RUSUH_DIR"/backups

hex() { openssl rand -hex "$1"; }

# --- kunci token Ed25519 (privat 0600, hanya terbaca user deploy; kontainer berjalan sebagai user itu) ---
for k in user queue payment booking; do
  if [ ! -f "$RUSUH_DIR/keys/$k-token.pem" ]; then
    openssl genpkey -algorithm ed25519 -out "$RUSUH_DIR/keys/$k-token.pem"
    openssl pkey -in "$RUSUH_DIR/keys/$k-token.pem" -pubout -out "$RUSUH_DIR/keys/$k-token.pub.pem"
    chmod 600 "$RUSUH_DIR/keys/$k-token.pem"; chmod 644 "$RUSUH_DIR/keys/$k-token.pub.pem"
    echo "kunci $k dibuat"
  fi
done

# --- nilai acak: dibuat sekali, disimpan di $RUSUH_DIR/env/.generated supaya semua file memakai nilai yang sama ---
GEN="$RUSUH_DIR/env/.generated"
if [ ! -f "$GEN" ]; then
  umask 077
  {
    echo "PII_ENC_KEY=$(hex 32)"
    echo "PII_HASH_KEY=$(hex 32)"
    echo "OTP_HASH_KEY=$(hex 32)"
    echo "TICKET_LINK_KEY=$(hex 32)"
    for n in POSTGRES_SUPERUSER QUEUE_DB BOOKING_DB PAYMENT_DB NOTIFICATION_DB USER_DB; do echo "${n}_PASSWORD=$(hex 24)"; done
    echo "RABBITMQ_PASSWORD=$(hex 24)"
    echo "VALKEY_PASSWORD=$(hex 24)"
    echo "GRAFANA_PASSWORD=$(hex 12)"
  } > "$GEN"
  echo "rahasia acak dibuat"
fi

domain="${DEV_DOMAIN:-}"
subst=()
while IFS='=' read -r k v; do [ -n "$k" ] && subst+=("$k=$v"); done < "$GEN"
[ -n "$domain" ]                    && subst+=("SET_DEV_DOMAIN=$domain" "SET_PAYMENT_SUCCESS_RETURN_URL=https://$domain/payment/success" \
                                                "SET_PAYMENT_FAILED_RETURN_URL=https://$domain/payment/failed" "SET_TICKET_LINK_BASE_URL=https://$domain/tickets")
[ -n "${ACME_EMAIL:-}" ]            && subst+=("SET_ACME_EMAIL=$ACME_EMAIL")
[ -n "${ADMIN_EMAILS:-}" ]          && subst+=("SET_ADMIN_EMAILS=$ADMIN_EMAILS")
[ -n "${MIDTRANS_SERVER_KEY:-}" ]   && subst+=("SET_MIDTRANS_SERVER_KEY=$MIDTRANS_SERVER_KEY")
[ -n "${RESEND_API_KEY:-}" ]        && subst+=("SET_RESEND_API_KEY=$RESEND_API_KEY")

render() { # template -> tujuan; mengganti @@NAMA@@ dengan nilainya
  local src="$1" dst="$2" line k v out
  umask 077
  : > "$dst"
  while IFS= read -r line || [ -n "$line" ]; do
    for kv in "${subst[@]}"; do k="${kv%%=*}"; v="${kv#*=}"; line="${line//@@$k@@/$v}"; done
    printf '%s\n' "$line" >> "$dst"
  done < "$src"
}

for tpl in "$HERE"/env/*.env.example; do
  name="$(basename "$tpl" .env.example)"
  dst="$RUSUH_DIR/env/$name.env"
  if [ -f "$dst" ]; then echo "lewati $name.env (sudah ada)"; continue; fi
  render "$tpl" "$dst"; chmod 600 "$dst"; echo "dibuat $name.env"
done

left="$(grep -lE '@@[A-Z_]+@@' "$RUSUH_DIR"/env/*.env 2>/dev/null || true)"
if [ -n "$left" ]; then
  echo; echo "MASIH ADA nilai yang harus diisi (cari '@@SET_' lalu ganti):"
  grep -HnE '@@[A-Z_]+@@' $left | sed 's/=.*@@/=@@/' || true
  echo "Edit file di atas (nano/vim), atau hapus file itu dan jalankan ulang skrip dengan variabel yang lengkap."
fi
echo "Grafana: user admin; password ada di $GEN (GRAFANA_PASSWORD), tampilkan dengan: grep GRAFANA_PASSWORD $GEN"
