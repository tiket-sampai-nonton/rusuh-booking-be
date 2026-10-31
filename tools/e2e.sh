#!/usr/bin/env bash
# E2E lintas service: membangun dan menjalankan 5 service (api + worker) di host terhadap infrastruktur Compose
# (make infra), lalu menjalankan tests/e2e. Setiap service membaca services/<svc>/.env (salin dari .env.example).
# Midtrans Snap Sandbox dipakai bila MIDTRANS_SERVER_KEY ada di services/payment/.env; email ke Mailpit.
#   tools/e2e.sh                 # skenario cepat (~1 menit)
#   E2E_SLOW=1 tools/e2e.sh      # termasuk expiry + settlement terlambat (~5 menit)
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
work="$(mktemp -d)"
keys="$root/deployments/compose/keys"
pids=()
cleanup() { for p in "${pids[@]:-}"; do kill "$p" 2>/dev/null || true; done; wait 2>/dev/null || true; rm -rf "$work"; }
trap cleanup EXIT

[ -f "$keys/queue-token.pem" ] || make -C "$root" keys
for s in queue booking payment notification user; do
  [ -f "$root/services/$s/.env" ] || { echo "services/$s/.env tidak ada (salin dari .env.example)" >&2; exit 1; }
  (cd "$root/services/$s" && GOWORK=off go build -o "$work/$s" ./cmd)
  (cd "$root/services/$s" && "$work/$s" migrate up >/dev/null)
done

# Booking butuh kunci service ke Payment dan allow-list admin (hanya domain uji lokal).
booking_env=(SERVICE_TOKEN_PRIVATE_KEY_PATH="$keys/booking-token.pem" PAYMENT_TOKEN_PUBLIC_KEY_PATH="$keys/payment-token.pub.pem"
  ADMIN_EMAILS="@admin.e2e.test")

start() { # svc verb port [ENV=val...]
  local svc=$1 verb=$2 port=$3; shift 3
  (cd "$root/services/$svc" && exec env "$@" APP_PORT="$port" "$work/$svc" "$verb" >"$work/$svc-$verb.log" 2>&1) & # exec: $! adalah biner itu sendiri, jadi kill menghentikannya
  pids+=("$!")
}
start queue api 8081;        start queue worker 8091
start booking api 8082 "${booking_env[@]}"; start booking worker 8092 "${booking_env[@]}"
start payment api 8083;      start payment worker 8093
start notification api 8084; start notification worker 8094
start user api 8085

for port in 8081 8082 8083 8084 8085; do
  for _ in $(seq 1 40); do curl -fsS "localhost:$port/health" >/dev/null 2>&1 && break; sleep 0.5; done
  curl -fsS "localhost:$port/health" >/dev/null || { echo "service :$port tidak sehat; lihat $work/*.log" >&2; cat "$work"/*.log | tail -30 >&2; exit 1; }
done

cd "$root/tests/e2e"
GOWORK=off go test -tags e2e -count=1 -timeout 15m "$@" ./...
