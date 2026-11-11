#!/usr/bin/env bash
# Uji pengaman rilis AWS tanpa menyentuh AWS: smoke-test.sh melawan server HTTP tiruan, dan check-ssm-env.sh dengan `aws`
# tiruan. Jalankan: bash deployments/terraform/guards_test.sh   (butuh python3 dan curl)
set -uo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
TMP="$(mktemp -d)"
SRV_PID=""
trap 'stop; rm -rf "$TMP"' EXIT
pass=0; fail=0
ok()  { pass=$((pass+1)); echo "  ok    $1"; }
bad() { fail=$((fail+1)); echo "  GAGAL $1"; }
expect() { # nama, kode keluar yang diharapkan, kode keluar sebenarnya
  [ "$2" = "$3" ] && ok "$1" || bad "$1 (diharapkan exit $2, dapat $3)"
}

cat > "$TMP/server.py" <<'PY'
import http.server, json, sys
routes = json.load(open(sys.argv[2]))
class H(http.server.BaseHTTPRequestHandler):
    def log_message(self, *a): pass
    def do_GET(self):
        code, kind = routes.get(self.path.split("?")[0].replace("/api/v1", "", 1), [404, "alb"])
        self.send_response(code)
        if kind == "json": body = b'{"meta":{"code":%d}}' % code; self.send_header("Content-Type", "application/json")
        elif kind == "html": body = b"<html>502 Bad Gateway</html>"; self.send_header("Content-Type", "text/html")
        else: body = b"Not found"; self.send_header("Content-Type", "text/plain")
        self.end_headers(); self.wfile.write(body)
http.server.HTTPServer(("127.0.0.1", int(sys.argv[1])), H).serve_forever()
PY
ZERO=00000000-0000-0000-0000-000000000000
good='{"/events":[200,"json"],"/queue-status/'$ZERO'":[404,"json"],"/me":[401,"json"],"/payments/'$ZERO'/status":[401,"json"],"/push-subscriptions":[405,"alb"]}'
stop() { [ -n "$SRV_PID" ] && { kill "$SRV_PID" 2>/dev/null; wait "$SRV_PID" 2>/dev/null; SRV_PID=""; }; }
serve() { # json rute -> mulai server di port acak
  stop
  echo "$1" > "$TMP/routes.json"; PORT=$((20000 + RANDOM % 20000))
  python3 "$TMP/server.py" "$PORT" "$TMP/routes.json" & SRV_PID=$!
  for _ in $(seq 1 30); do curl -s -o /dev/null "http://127.0.0.1:$PORT/x" && break; sleep 0.1; done
}
variant() { # path kode jenis -> rute dasar dengan satu rute diganti
  python3 -c 'import json,sys;d=json.loads(sys.argv[1]);d[sys.argv[2]]=[int(sys.argv[3]),sys.argv[4]];print(json.dumps(d))' "$good" "$1" "$2" "$3"
}
smoke() { API_BASE="http://127.0.0.1:$PORT" SMOKE_TRIES=2 SMOKE_SLEEP=0 bash "$HERE/smoke-test.sh" > "$TMP/out.txt" 2>&1; echo $?; }

echo "smoke-test.sh"
serve "$good";                                                       expect "semua service menjawab: lolos" 0 "$(smoke)"
serve "$(variant "/queue-status/$ZERO" 502 html)"
r=$(smoke); expect "queue 502 walau katalog 200 (insiden prod): gagal" 1 "$r"; grep -q "queue=502" "$TMP/out.txt" && ok "pesan menyebut queue=502" || bad "pesan tidak menyebut queue=502"
serve "$(variant "/queue-status/$ZERO" 200 json)";                   expect "queue 200 (event ada): lolos" 0 "$(smoke)"
serve "$(variant "/queue-status/$ZERO" 404 alb)"
r=$(smoke); expect "404 polos dari ALB bukan jawaban queue: gagal" 1 "$r"; grep -q "bukan-jawaban-service" "$TMP/out.txt" && ok "pesan membedakan jawaban ALB dari service" || bad "pesan tidak membedakan"
serve "$(variant "/me" 200 json)";                                   expect "user 200 tanpa token (tidak mungkin): gagal" 1 "$(smoke)"
serve "$(variant "/events" 502 html)";                               expect "booking 502: gagal" 1 "$(smoke)"
serve "$(variant "/push-subscriptions" 503 html)";                   expect "notification 503: gagal" 1 "$(smoke)"
stop; PORT=1;                                                        expect "tidak ada server (000): gagal" 1 "$(smoke)"

echo; echo "check-ssm-env.sh"
mkdir -p "$TMP/bin" "$TMP/ssm"
cat > "$TMP/bin/aws" <<'AWS'
#!/usr/bin/env bash
# aws tiruan: hanya `ssm get-parameter --name /rusuh-test/<svc>.env`
name=""; while [ $# -gt 0 ]; do [ "$1" = "--name" ] && name="$2"; shift; done
if [ "${FAKE_AWS_DENY:-}" = 1 ]; then echo "An error occurred (AccessDeniedException) when calling the GetParameter operation: User is not authorized to perform: ssm:GetParameter" >&2; exit 254; fi
f="$FAKE_SSM/$(basename "$name")"
[ -f "$f" ] && { cat "$f"; exit 0; }
echo "An error occurred (ParameterNotFound) when calling the GetParameter operation: " >&2; exit 254
AWS
chmod +x "$TMP/bin/aws"
tplsvc() { grep -oE '^[A-Z_][A-Z0-9_]*=' "$HERE/../dev/env/$1.env.example" | sed 's/$/SECRETMARK123/'; }
fresh() { rm -f "$TMP"/ssm/*; for s in queue booking payment notification user; do tplsvc $s > "$TMP/ssm/$s.env"; done; }
ssmcheck() { PATH="$TMP/bin:$PATH" FAKE_SSM="$TMP/ssm" ENV_NAME=test bash "$HERE/check-ssm-env.sh" > "$TMP/ssmout.txt" 2>&1; echo $?; }
fresh;                                                          expect "env lengkap terhadap template: lolos" 0 "$(ssmcheck)"
grep -q "SECRETMARK123" "$TMP/ssmout.txt" && bad "nilai env ikut tercetak" || ok "nilai env tidak pernah dicetak"
fresh; sed -i.bak '/^BOOKING_PAGE_BASE_URL=/d' "$TMP/ssm/queue.env"
r=$(ssmcheck); expect "BOOKING_PAGE_BASE_URL hilang (insiden prod): gagal" 1 "$r"; grep -q "queue.env.*BOOKING_PAGE_BASE_URL" "$TMP/ssmout.txt" && ok "pesan menyebut service dan nama key" || bad "pesan tidak menyebut key"
fresh; sed -i.bak '/^CORS_ALLOWED_ORIGINS=/d' "$TMP/ssm/user.env";        expect "key opsional terdaftar hilang: tetap lolos" 0 "$(ssmcheck)"
fresh; sed -i.bak '/^PAYMENT_MAX_ATTEMPTS=/d;/^MIDTRANS_SERVER_KEY=/d' "$TMP/ssm/payment.env"; r=$(ssmcheck); expect "key wajib lain hilang: gagal" 1 "$r"
fresh; rm -f "$TMP/ssm/booking.env";                                    expect "parameter tidak ada: gagal" 1 "$(ssmcheck)"
fresh; r=$(FAKE_AWS_DENY=1 ssmcheck);                                   expect "kredensial CI tanpa izin baca: dilewati (exit 2)" 2 "$r"

echo; echo "hasil: $pass lolos, $fail gagal"; [ "$fail" -eq 0 ]
