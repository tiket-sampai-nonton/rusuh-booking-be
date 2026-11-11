#!/usr/bin/env bash
# Membandingkan NAMA key di env/*.env.example dengan file env di server ($RUSUH_DIR/env). gen-secrets.sh tidak pernah
# menimpa file yang sudah ada, jadi env wajib baru di template tidak sampai ke server dan service gagal start (D-24).
# Hanya nama key yang dicetak, tidak pernah nilainya. Exit 1 bila ada key yang hilang.
#   bash check-env.sh            # dipakai deploy.sh dan preflight.sh
set -uo pipefail
RUSUH_DIR="${RUSUH_DIR:-/opt/rusuh}"
HERE="$(cd "$(dirname "$0")" && pwd)"
fail=0
for tpl in "$HERE"/env/*.env.example; do
  name="$(basename "$tpl" .env.example)"
  dst="$RUSUH_DIR/env/$name.env"
  [ -f "$dst" ] || continue # file yang belum ada dilaporkan oleh pemanggil
  missing="$(comm -23 <(grep -oE '^[A-Z_][A-Z0-9_]*=' "$tpl" | tr -d = | sort -u) \
                      <(grep -oE '^[A-Z_][A-Z0-9_]*=' "$dst" | tr -d = | sort -u) | tr '\n' ' ')"
  if [ -n "$missing" ]; then
    echo "$name.env belum punya key dari template: $missing(salin dari env/$name.env.example lalu isi nilainya)"
    fail=1
  fi
done
exit "$fail"
