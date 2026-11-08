#!/usr/bin/env bash
# E2E pembelian tiket sebagai guest lewat curl terhadap API publik (mengikuti panduan-testing/PANDUAN-E2E-TESTING-DEV.md bagian 5-8).
#   BASE=https://api.rusuhbooking.my.id EVENT_ID=<uuid> EMAIL=<email pembeli> NIK_BUYER=<16 digit> NIK_ATTENDEE=<16 digit> \
#     bash aws-e2e.sh buy      # join antrean (tanpa data) -> claim -> sesi -> hold (+data pembeli) -> attempt pembayaran
#   bash aws-e2e.sh wait       # tunggu webhook Midtrans mengubah booking menjadi PAID, lalu tampilkan e-tiket
# Opsional: CATEGORY_CODE (bawaan Festival), METHOD (VA_BCA|VA_BNI|VA_BRI|VA_PERMATA|QRIS|GOPAY, bawaan VA_BCA).
# Token tidak pernah dicetak; state disimpan di $E2E_STATE (izin 600). Data uji saja: jangan pakai NIK atau email asli milik orang lain.
set -euo pipefail
: "${BASE:?}"
STATE="${E2E_STATE:-${TMPDIR:-/tmp}/rusuh-e2e-state.json}"

# jq tidak diwajibkan: python3 sudah ada di macOS/Linux. get <path> membaca JSON dari stdin (a.b.0.c).
get() { python3 -I -c 'import sys,json
d=json.load(sys.stdin)
for k in sys.argv[1].split("."):
    d=d[k] if isinstance(d,dict) else d[int(k)]
print(d)' "$1"; }
# show: cetak data tanpa field token (nilai kunci apa pun yang mengandung "token" disamarkan).
show() { python3 -I -c 'import sys,json
def mask(x):
    if isinstance(x,dict): return {k:("***" if "token" in k.lower() else mask(v)) for k,v in x.items()}
    if isinstance(x,list): return [mask(i) for i in x]
    return x
d=json.load(sys.stdin); print(json.dumps(mask(d.get("data",d)),indent=2,ensure_ascii=False))'; }
save() { python3 -I -c 'import sys,json,os
p=sys.argv[1]; k=sys.argv[2]; v=sys.argv[3]
d=json.load(open(p)) if os.path.exists(p) else {}
d[k]=v
fd=os.open(p,os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
with os.fdopen(fd,"w") as f: json.dump(d,f)' "$STATE" "$1" "$2"; }
load() { python3 -I -c 'import sys,json; print(json.load(open(sys.argv[1]))[sys.argv[2]])' "$STATE" "$1"; }

# call METHOD PATH [BODY] [header...]  -> badan ke stdout; berhenti dengan kode+pesan bila bukan 2xx
call() {
  local method="$1" path="$2" body="${3:-}"; shift 3 || shift $#
  local args=(-sS -m 30 -X "$method" -w '\n%{http_code}' -H 'Content-Type: application/json')
  for h in "$@"; do args+=(-H "$h"); done
  [ -z "$body" ] || args+=(-d "$body")
  local out code
  out="$(curl "${args[@]}" "$BASE$path")"
  code="${out##*$'\n'}"; out="${out%$'\n'*}"
  case "$code" in 2*) printf '%s' "$out" ;;
    *) echo "GAGAL $method $path -> HTTP $code: $(printf '%s' "$out" | python3 -I -c 'import sys,json
try: print(json.load(sys.stdin)["meta"])
except Exception: print("(bukan JSON)")')" >&2; return 1 ;;
  esac
}

case "${1:-}" in
buy)
  : "${EVENT_ID:?}" "${EMAIL:?}" "${NIK_BUYER:?}" "${NIK_ATTENDEE:?}"
  CATEGORY_CODE="${CATEGORY_CODE:-Festival}"; METHOD="${METHOD:-VA_BCA}"
  rm -f "$STATE"

  echo "1. katalog: cari kategori $CATEGORY_CODE"
  CATEGORY_ID="$(call GET "/api/v1/events/$EVENT_ID" "" | python3 -I -c 'import sys,json
d=json.load(sys.stdin)["data"]
c=[x for x in d["categories"] if x["code"]==sys.argv[1]]
print(c[0]["categoryId"] if c else "")' "$CATEGORY_CODE")"
  [ -n "$CATEGORY_ID" ] || { echo "kategori $CATEGORY_CODE tidak ada" >&2; exit 1; }

  echo "2. join antrean (guest)"
  # ADR-030: join tidak membawa NIK/email/consent (body diabaikan server); data pembeli dikirim saat hold.
  JOIN="$(call POST "/api/v1/events/$EVENT_ID/queue-entries" '{}' 'X-Client-Type: mobile')"
  ENTRY_ID="$(printf '%s' "$JOIN" | get data.entryId)"; VT="$(printf '%s' "$JOIN" | get data.visitorToken)"
  save entryId "$ENTRY_ID"; save visitorToken "$VT"; save eventId "$EVENT_ID"
  printf '%s' "$JOIN" | show

  echo "3. tunggu ADMITTED"
  status=""
  for i in $(seq 1 40); do
    ME="$(call GET "/api/v1/queue-entries/me?eventId=$EVENT_ID" "" "X-Visitor-Token: $VT")"
    status="$(printf '%s' "$ME" | get data.status)"; echo "   [$i] $status"
    [ "$status" = ADMITTED ] && break
    sleep 3
  done
  [ "$status" = ADMITTED ] || { echo "tidak ADMITTED dalam 2 menit (cek log queue-worker)" >&2; exit 1; }

  echo "4. claim (admission token hanya berlaku 60 detik)"
  ADM="$(call POST "/api/v1/queue-entries/$ENTRY_ID/claim?eventId=$EVENT_ID" "" "X-Visitor-Token: $VT" | get data.admissionToken)"

  echo "5. sesi booking"
  SES="$(call POST /api/v1/booking-sessions "{\"admissionToken\":\"$ADM\"}" "X-Visitor-Token: $VT")"
  SESSION_ID="$(printf '%s' "$SES" | get data.sessionId)"; save sessionId "$SESSION_ID"
  printf '%s' "$SES" | show

  echo "6. hold tiket"
  HOLD="$(call POST /api/v1/bookings \
    "{\"sessionId\":\"$SESSION_ID\",\"buyer\":{\"nik\":\"$NIK_BUYER\",\"email\":\"$EMAIL\",\"emailConfirmation\":\"$EMAIL\",\"consent\":true},\"items\":[{\"categoryId\":\"$CATEGORY_ID\",\"attendee\":{\"nik\":\"$NIK_ATTENDEE\",\"fullName\":\"Peserta Uji\"}}]}" \
    "X-Visitor-Token: $VT" "Idempotency-Key: e2e-hold-$SESSION_ID")"
  BOOKING_ID="$(printf '%s' "$HOLD" | get data.bookingId)"; save bookingId "$BOOKING_ID"
  printf '%s' "$HOLD" | show

  echo "7. attempt pembayaran ($METHOD)"
  ATT="$(call POST "/api/v1/payments/$BOOKING_ID/attempts" "{\"method\":\"$METHOD\"}" "X-Visitor-Token: $VT" "Idempotency-Key: e2e-attempt-$SESSION_ID")"
  printf '%s' "$ATT" | show
  save redirectUrl "$(printf '%s' "$ATT" | get data.redirectUrl 2>/dev/null || true)"
  echo
  echo "SELESAI tahap beli. bookingId=$BOOKING_ID"
  echo "Bayar sekarang (batas ~10 menit): buka redirectUrl di atas, pilih metode, lalu selesaikan di https://simulator.sandbox.midtrans.com"
  echo "Setelah itu: bash aws-e2e.sh wait"
  ;;
wait)
  VT="$(load visitorToken)"; BOOKING_ID="$(load bookingId)"
  echo "menunggu PAID untuk booking $BOOKING_ID (maks 15 menit, cek tiap 5 detik)"
  last=""
  for i in $(seq 1 180); do
    ST="$(call GET "/api/v1/payments/$BOOKING_ID/status" "" "X-Visitor-Token: $VT")"
    cur="$(printf '%s' "$ST" | python3 -I -c 'import sys,json; d=json.load(sys.stdin)["data"]; print(d.get("bookingStatus"))')"
    [ "$cur" = "$last" ] || { echo "   [$i] $cur"; last="$cur"; }
    case "$cur" in PAID*) break ;; EXPIRED*|CANCELLED*|FAILED*) echo "berakhir tanpa PAID: $cur" >&2; exit 1 ;; esac
    sleep 5
  done
  case "$cur" in PAID*) ;; *) echo "belum PAID setelah 15 menit" >&2; exit 1 ;; esac
  echo "e-tiket:"
  call GET "/api/v1/bookings/$BOOKING_ID/tickets" "" "X-Visitor-Token: $VT" | python3 -I -c 'import sys,json
d=json.load(sys.stdin)["data"]
ts=d.get("tickets",d if isinstance(d,list) else [])
print("jumlah tiket:",len(ts))
for t in ts: print({k:(str(v)[:7]+"...") if k in ("code","ticketCode","qrPayload") else (v if not isinstance(v,str) or len(v)<60 else v[:20]+"...") for k,v in t.items()})'
  echo "Cek inbox email pembeli untuk e-tiket (QR)."
  ;;
*) echo "pakai: aws-e2e.sh buy|wait" >&2; exit 2 ;;
esac
