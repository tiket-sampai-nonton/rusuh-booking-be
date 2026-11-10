#!/usr/bin/env bash
# Jalankan satu jenis tes k6. Hasil disimpan di folder tetap per jenis dan MENIMPA run sebelumnya:
#   tests/k6/reports/<TEST_ENV>/<jenis>/
#     summary.json      metrik agregat k6 + konteks uji (handleSummary)
#     metrics.json.gz   metrik mentah per sampel (k6 --out json; tidak di-commit)
#     report.html       dashboard deret waktu bawaan k6 (web dashboard export)
#     analysis.html     ringkasan eksekutif, analisis kapasitas, bottleneck (scripts/report.mjs)
#     capacity.md, result.json, levels.csv, endpoints.csv, timeseries.csv
#     resources.csv, infra.csv   CPU/memori container, koneksi DB, backlog RabbitMQ, Valkey (lokal saja)
#     run.json          commit, hash skrip, parameter
# Riwayat semua run (benchmark) tidak ikut tertimpa: satu baris per run di tests/k6/reports/<TEST_ENV>/RINGKASAN.md.
#
#   bash tests/k6/scripts/run.sh <smoke|e2e|concurrency|load|stress|spike|soak|waiting|all>
# Env: TEST_ENV (local|dev|prod), BASE_URL, ADMIN_URL, AUTH_TOKEN, LOAD_FACTOR, SOAK_MINUTES, SLO_*, CONFIRM_PROD=yes.
set -uo pipefail
K6DIR="$(cd "$(dirname "$0")/.." && pwd)"
REPO="$(cd "$K6DIR/../.." && pwd)"
TYPE="${1:?pakai: run.sh <smoke|e2e|concurrency|load|stress|spike|soak|waiting|all>}"
export TEST_ENV="${TEST_ENV:-local}"

if [ "$TYPE" = all ]; then
  # Smoke harus lulus dulu; soak tidak ikut (jalankan terpisah karena lama).
  bash "$0" smoke || { echo "smoke gagal: tes beban tidak dijalankan" >&2; exit 1; }
  rc=0
  for t in e2e concurrency load stress spike; do bash "$0" "$t" || rc=1; done
  exit $rc
fi

case "$TYPE" in
  smoke | e2e | concurrency) SCRIPT="scenarios/$TYPE.js" ;;
  load | stress | spike | soak) SCRIPT="scenarios/traffic.js" ;;
  waiting) SCRIPT="scenarios/waiting-room.js" ;;
  *) echo "jenis tes tidak dikenal: $TYPE" >&2; exit 2 ;;
esac
if [ "$TEST_ENV" = prod ] && [ "${CONFIRM_PROD:-}" != yes ]; then
  echo "uji ke prod butuh persetujuan eksplisit: CONFIRM_PROD=yes" >&2; exit 2
fi
# Lokal: server key Sandbox untuk menandatangani webhook uji (nilai tidak pernah dicetak).
if [ "$TEST_ENV" = local ] && [ -z "${MIDTRANS_SERVER_KEY:-}" ] && [ -f "$REPO/services/payment/.env" ]; then
  MIDTRANS_SERVER_KEY="$(grep -m1 '^MIDTRANS_SERVER_KEY=' "$REPO/services/payment/.env" | cut -d= -f2-)"
  export MIDTRANS_SERVER_KEY
fi

OUT="$K6DIR/reports/$TEST_ENV/$TYPE"
rm -rf "$OUT" && mkdir -p "$OUT" # hasil terbaru menimpa run sebelumnya; riwayat ada di RINGKASAN.md
export REPORT_DIR="$OUT" RUN_ID="$(date +%H%M%S)" TEST_TYPE="$TYPE"
export SCRIPT_PATH="tests/k6/$SCRIPT" # dicatat di summary.json

python3 -I - "$OUT/run.json" "$REPO" "$K6DIR" "$TYPE" "$SCRIPT" <<'PY'
import hashlib, json, os, subprocess, sys
out, repo, k6dir, typ, script = sys.argv[1:]
git = lambda *a: subprocess.run(['git', '-C', repo, *a], capture_output=True, text=True).stdout.strip()
files = sorted(os.path.join(r, f) for r, _, fs in os.walk(k6dir) for f in fs
               if f.endswith(('.js', '.sh', '.mjs', '.yaml', '.html')) and '/reports/' not in r + '/')
h = hashlib.sha256(b''.join(open(f, 'rb').read() for f in files)).hexdigest()[:16]
keep = ('TEST_ENV', 'BASE_URL', 'ADMIN_URL', 'LOAD_FACTOR', 'SOAK_MINUTES', 'PROFILE', 'VUS', 'RAMP', 'HOLD',
        'E2E_VUS', 'E2E_ITERATIONS', 'PARALLEL', 'SLO_P95_MS', 'SLO_P99_MS', 'SLO_ERROR_RATE', 'SLO_CHECK_RATE')
json.dump({'type': typ, 'script': f'tests/k6/{script}', 'commit': git('rev-parse', 'HEAD'),
           'dirty': bool(git('status', '--porcelain', '--', '.')), 'k6Hash': h,
           'k6Version': subprocess.run(['k6', 'version'], capture_output=True, text=True).stdout.split('\n')[0],
           'params': {k: os.environ[k] for k in keep if k in os.environ}}, open(out, 'w'), indent=1)
PY

# Sampler sumber daya lokal (docker): container per 5 detik, infra (DB/MQ/Valkey) di putaran yang sama.
SAMPLER=""
if [ "$TEST_ENV" = local ]; then
  (
    echo "ts,container,cpu_pct,mem" > "$OUT/resources.csv"
    echo "ts,pg_connections,pg_lock_waits,mq_backlog,valkey_mem_mb,valkey_ops,valkey_clients" > "$OUT/infra.csv"
    while :; do
      ts=$(date +%s)
      docker stats --no-stream --format '{{.Name}},{{.CPUPerc}},{{.MemUsage}}' $(docker ps -q --filter name=rusuh-booking-be-) 2>/dev/null |
        sed -e "s/^/$ts,/" -e 's/%,/,/' -e 's/ \/ .*//' >> "$OUT/resources.csv"
      pg=$(echo "select count(*), count(*) filter (where wait_event_type = 'Lock') from pg_stat_activity where backend_type='client backend';" |
        docker exec -i rusuh-booking-be-postgres-1 sh -c 'psql -U "$POSTGRES_USER" -d "${POSTGRES_DB:-postgres}" -At -F,' 2>/dev/null)
      mq=$(docker exec rusuh-booking-be-rabbitmq-1 rabbitmqctl -q list_queues messages consumers 2>/dev/null | awk '$2>0 {s+=$1} END {print s+0}')
      vk=$(docker exec rusuh-booking-be-valkey-1 valkey-cli info 2>/dev/null | tr -d '\r' |
        awk -F: '/^used_memory:/{m=$2/1048576} /^instantaneous_ops_per_sec:/{o=$2} /^connected_clients:/{c=$2} END{printf "%.1f,%s,%s", m, o, c}')
      echo "$ts,${pg:-,},${mq:-},$vk" >> "$OUT/infra.csv"
      sleep 4
    done
  ) &
  SAMPLER=$!
fi

echo "== $TYPE @ $TEST_ENV -> ${OUT#"$REPO"/}"
K6_WEB_DASHBOARD=true K6_WEB_DASHBOARD_EXPORT="$OUT/report.html" K6_WEB_DASHBOARD_PORT=-1 \
  k6 run --no-usage-report --summary-time-unit=ms \
  --system-tags=status,method,name,scenario,group,check,expected_response,error_code \
  --out "json=$OUT/metrics.json.gz" "$K6DIR/$SCRIPT" 2>&1 | tee "$OUT/k6.log" | grep -vE '^\s*(running|default|browse|waiters|buy|account|smoke|e2e|concurrency|waiting) '
RC=${PIPESTATUS[0]}

[ -n "$SAMPLER" ] && kill "$SAMPLER" 2>/dev/null
wait 2>/dev/null
node "$K6DIR/scripts/report.mjs" "$OUT" || echo "pembuatan laporan gagal (lihat pesan di atas)" >&2
echo "k6 exit $RC; laporan: ${OUT#"$REPO"/}/analysis.html"
exit "$RC"
