#!/usr/bin/env bash
# Membandingkan NAMA key di deployments/dev/env/<service>.env.example dengan parameter SSM /rusuh-<env>/<service>.env.
# seed-ssm.sh membuat parameter itu sekali dari template; sesudahnya template berubah tanpa SSM ikut berubah, dan env wajib
# baru membuat service gagal start (insiden prod 2026-11-11: BOOKING_PAGE_BASE_URL). Hanya nama key yang dicetak.
#   ENV_NAME=prod AWS_REGION=ap-southeast-2 bash check-ssm-env.sh
# Kode keluar: 0 lengkap | 1 ada key hilang atau parameter tidak terbaca | 2 kredensial tidak boleh membaca (dilewati).
# Kode 2 normal untuk user CI: ia sengaja tidak boleh membaca env (isinya semua rahasia), jadi cek ini dijalankan operator.
set -uo pipefail
: "${ENV_NAME:?}"
name="rusuh-$ENV_NAME"
here="$(cd "$(dirname "$0")" && pwd)"
tpl="$here/../dev/env"
optional="$here/ssm-env-optional.txt"
failed=0
for svc in queue booking payment notification user; do
  if ! value="$(aws ssm get-parameter --name "/$name/$svc.env" --with-decryption --query Parameter.Value --output text 2>&1)"; then
    if printf '%s' "$value" | grep -qiE 'AccessDenied|not authorized'; then
      echo "cek env SSM dilewati: kredensial ini tidak boleh membaca /$name/$svc.env" >&2; exit 2
    fi
    echo "gagal membaca /$name/$svc.env: $(printf '%s' "$value" | head -1)" >&2; exit 1
  fi
  have="$(printf '%s\n' "$value" | grep -oE '^[A-Z_][A-Z0-9_]*=' | tr -d = | sort -u)"
  want="$(grep -oE '^[A-Z_][A-Z0-9_]*=' "$tpl/$svc.env.example" | tr -d = | sort -u)"
  opt="$(grep -vE '^\s*#' "$optional" | awk -v s="$svc" '$1 == s {print $2}' | sort -u)"
  missing="$(comm -23 <(printf '%s\n' "$want") <(printf '%s\n' "$have") | comm -23 - <(printf '%s\n' "$opt") | tr '\n' ' ')"
  if [ -n "$missing" ]; then
    echo "/$name/$svc.env belum punya key wajib dari template: ${missing}(tambahkan ke SSM, atau daftarkan di ssm-env-optional.txt bila memang opsional)" >&2
    failed=1
  fi
done
[ "$failed" -eq 0 ] && echo "env SSM lengkap terhadap template (kecuali yang terdaftar opsional)"
exit "$failed"
