#!/usr/bin/env bash
# Mengukur coverage logika satu modul Go dan gagal bila di bawah ambang.
# Dikecualikan dari hitungan: cmd/, delivery/, config/, _mock/ (bootstrap dan wiring; dibuktikan
# lewat run dan health check, bukan unit test).
# Pakai: tools/check-coverage.sh <path-modul> [ambang=80]
set -euo pipefail

dir="${1:?path modul, mis. services/booking}"
min="${2:-80}"
root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
name="$(basename "$dir")"

mkdir -p "$root/coverage"
raw="$(mktemp)"
trap 'rm -f "$raw"' EXIT

(cd "$root/$dir" && go test -race -count=1 -covermode=atomic -coverprofile="$raw" ./...)

out="$root/coverage/${name}.out"
grep -v -E '/(cmd|_mock|delivery|config)/' "$raw" > "$out"

pct="$(cd "$root/$dir" && go tool cover -func="$out" | awk '/^total:/ {gsub("%", "", $NF); print $NF}')"
if [ -z "$pct" ]; then
  echo "::error::$name: tidak bisa menghitung coverage"
  exit 1
fi

line="$name: coverage logika ${pct}% (ambang ${min}%)"
echo "$line"
if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  echo "- $line" >> "$GITHUB_STEP_SUMMARY"
fi

if ! awk -v p="$pct" -v m="$min" 'BEGIN { exit !(p + 0 >= m + 0) }'; then
  echo "::error::$name: coverage ${pct}% di bawah ambang ${min}%"
  exit 1
fi
