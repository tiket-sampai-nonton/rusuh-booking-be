#!/usr/bin/env bash
# Smoke test lewat jalur publik untuk SEMUA service, bukan hanya katalog. `/events` dilayani Booking, jadi dulu rilis tetap
# "hijau" walau queue-api mati (queue-status 502). Setiap rute di bawah harus dijawab service-nya sendiri: kode yang
# diharapkan DAN badan JSON `{"meta":...}` (404 polos dari ALB atau 502 dari target mati tidak lolos).
#   API_DOMAIN=api.rusuhbooking.my.id bash smoke-test.sh          # dipakai deploy-aws.sh dan aws-power.sh on
#   API_BASE=http://localhost:8080 bash smoke-test.sh              # untuk uji (tanpa TLS)
# SMOKE_TRIES (bawaan 18) dan SMOKE_SLEEP detik (bawaan 10) mengatur lama menunggu service naik.
set -uo pipefail
BASE="${API_BASE:-https://${API_DOMAIN:?API_DOMAIN atau API_BASE wajib}}/api/v1"
TRIES="${SMOKE_TRIES:-18}"
SLEEP="${SMOKE_SLEEP:-10}"
ZERO=00000000-0000-0000-0000-000000000000
body="$(mktemp)"
trap 'rm -f "$body"' EXIT

# probe <nama> <path> <kode yang diterima, dipisah |> <wajib JSON meta: 1|0>
probe() {
  local code
  code="$(curl -s -m 10 -o "$body" -w '%{http_code}' "$BASE$2" 2>/dev/null || echo 000)"
  case "|$3|" in *"|$code|"*) ;; *) failed+=" $1=$code"; return ;; esac
  if [ "$4" = 1 ] && ! grep -q '"meta"' "$body"; then failed+=" $1=$code(bukan-jawaban-service)"; fi
}

for try in $(seq 1 "$TRIES"); do
  failed=""
  probe booking "/events" 200 1
  probe queue "/queue-status/$ZERO" "200|404" 1
  probe user "/me" 401 1
  probe payment "/payments/$ZERO/status" 401 1
  probe notification "/push-subscriptions" "401|404|405" 0
  if [ -z "$failed" ]; then echo "smoke test lulus (booking, queue, user, payment, notification dijawab service masing-masing)"; exit 0; fi
  echo "smoke test $try/$TRIES, belum siap:$failed"
  [ "$try" -lt "$TRIES" ] && sleep "$SLEEP"
done
echo "smoke test gagal:$failed" >&2
exit 1
