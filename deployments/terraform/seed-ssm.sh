#!/usr/bin/env bash
# Membuat semua rahasia AWS sekali jalan di SSM, dari template yang sama dengan dev. Dijalankan di laptop, SEBELUM boot pertama:
#   export ENV_NAME=prod AWS_REGION=ap-southeast-2 API_DOMAIN=api.rusuhbooking.my.id IMAGES_BUCKET=<output images_bucket>
#   export ADMIN_EMAILS=... MIDTRANS_SERVER_KEY=... RESEND_API_KEY=...
#   bash deployments/terraform/seed-ssm.sh
# Berkas sementara dihapus saat selesai; nilainya hanya tersimpan di SSM. Menolak jalan bila rahasia sudah ada,
# karena password yang baru tidak akan cocok dengan database yang sudah dibuat.
set -euo pipefail
: "${ENV_NAME:?}" "${AWS_REGION:?}" "${API_DOMAIN:?}" "${IMAGES_BUCKET:?}" "${ADMIN_EMAILS:?}" "${MIDTRANS_SERVER_KEY:?}" "${RESEND_API_KEY:?}"
export AWS_REGION AWS_DEFAULT_REGION="$AWS_REGION" ADMIN_EMAILS MIDTRANS_SERVER_KEY RESEND_API_KEY
name="rusuh-$ENV_NAME"
# Pengaman salah ketik: bucket harus ada, dan Sandbox Midtrans harus menerima kuncinya (404 = kunci sah, order uji tidak ada;
# 401 = kunci salah atau milik Production). Format awalan tidak dipakai: akun berbeda bisa memakai SB-Mid-server- atau Mid-server-.
aws s3api head-bucket --bucket "$IMAGES_BUCKET" || { echo "bucket $IMAGES_BUCKET tidak ditemukan (nomor akun kosong?)" >&2; exit 1; }
code="$(curl -s -m 15 -o /dev/null -w '%{http_code}' -u "$MIDTRANS_SERVER_KEY:" https://api.sandbox.midtrans.com/v2/rusuh-cek-kunci/status || true)"
[ "$code" != 401 ] || { echo "Sandbox Midtrans menolak MIDTRANS_SERVER_KEY (401); ambil dari dashboard dengan toggle Sandbox" >&2; exit 1; }
dev="$(cd "$(dirname "$0")/../dev" && pwd)"

if aws ssm get-parameter --name "/$name/data.env" >/dev/null 2>&1; then
  echo "/$name/data.env sudah ada; hentikan agar password database tidak berubah. Ubah satu nilai lewat put-parameter --overwrite." >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
DEV_DOMAIN="$API_DOMAIN" RUSUH_DIR="$tmp" bash "$dev/gen-secrets.sh" >/dev/null

out="$tmp/ssm"
mkdir -p "$out"
for s in queue booking payment notification user; do
  # Baris pelonggaran rate limit milik VPS dev dihapus: di AWS IP klien terbaca, jadi nilai bawaan aplikasi yang berlaku.
  sed -E \
    -e "s/^APP_ENV=.*/APP_ENV=$ENV_NAME/" \
    -e 's/^POSTGRES_HOST=.*/POSTGRES_HOST=__DATA_HOST__/' \
    -e 's/"host":"valkey"/"host":"__DATA_HOST__"/' \
    -e 's/@rabbitmq:5672/@__DATA_HOST__:5672/' \
    -e '/^RATE_LIMIT_(PER_IP|JOIN_PER_IP)=/d' \
    -e '/^OTP_MAX_(PER_IP_HOUR|VERIFY_PER_IP_MIN)=/d' \
    -e '/^TOKEN_MAX_REFRESH_PER_IP_MIN=/d' \
    -e '/^#/d' \
    "$tmp/env/$s.env" > "$out/$s.env"
done
# Queue dan Booking ada di ASG berbeda; payment dan booking tetap saling panggil lewat nama container di ASG core.
sed -E 's#^BOOKING_SERVICE_URL=.*#BOOKING_SERVICE_URL=http://__BOOKING_INTERNAL__:8082#' "$out/queue.env" > "$out/q" && mv "$out/q" "$out/queue.env"
sed -E -e 's/^IMAGE_STORAGE=.*/IMAGE_STORAGE=s3/' -e '/^IMAGE_LOCAL_DIR=/d' \
  -e "s#^IMAGE_PUBLIC_BASE_URL=.*#IMAGE_PUBLIC_BASE_URL=https://$IMAGES_BUCKET.s3.$AWS_REGION.amazonaws.com#" "$out/booking.env" > "$out/b" && mv "$out/b" "$out/booking.env"
printf 'IMAGE_S3_BUCKET=%s\nAWS_REGION=%s\n' "$IMAGES_BUCKET" "$AWS_REGION" >> "$out/booking.env"
cat "$tmp/env/postgres.env" "$tmp/env/rabbitmq.env" "$tmp/env/valkey.env" | sed '/^#/d' > "$out/data.env"

# Pengaman: jangan sampai host khusus Compose atau placeholder yang belum terisi masuk ke SSM.
if grep -lE '@@[A-Z_]+@@|POSTGRES_HOST=postgres|"host":"valkey"|@rabbitmq:' "$out"/*.env; then
  echo "file di atas masih memuat nilai dev/placeholder; tidak ada yang dikirim" >&2
  exit 1
fi

for f in "$out"/*.env; do
  aws ssm put-parameter --type SecureString --name "/$name/$(basename "$f")" --value "file://$f" >/dev/null
  echo "/$name/$(basename "$f")"
done
for f in "$tmp"/keys/*.pem; do
  aws ssm put-parameter --type SecureString --name "/$name/keys/$(basename "$f")" --value "file://$f" >/dev/null
  echo "/$name/keys/$(basename "$f")"
done
