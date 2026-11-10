#!/usr/bin/env bash
# Quality gate untuk workflow /feature.
#   quality-gate.sh --baseline   catat kondisi awal repo (sebelum coding)
#   quality-gate.sh              jalankan gate; exit 0 = PASS, 1 = FAIL
#
# Check: syntax (file yang berubah), secrets (baris yang ditambah), test, lint, typecheck.
# test/lint/typecheck yang SUDAH gagal di baseline dianggap pre-existing (dilaporkan, tidak memblok),
# supaya repo legacy tetap bisa dipakai. Yang memblok hanya regresi.
set -uo pipefail

MODE="${1:-gate}"
SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(git rev-parse --show-toplevel 2>/dev/null)" || { echo "Bukan git repo."; exit 1; }
GIT_DIR="$(git rev-parse --absolute-git-dir)"
STATE="$GIT_DIR/feature-workflow"
mkdir -p "$STATE"
cd "$ROOT"

STACK="" TEST_CMD="" LINT_CMD="" TYPECHECK_CMD=""
# eval, bukan `source <(...)`: process substitution + source tidak jalan di bash 3.2 bawaan macOS
eval "$("$SCRIPTS/detect-stack.sh" "$ROOT")"
BASE_SHA="$(cat "$STATE/base_sha" 2>/dev/null || git rev-parse HEAD 2>/dev/null || echo "")"

changed_files() {
  { [ -n "$BASE_SHA" ] && git diff --name-only --diff-filter=ACMR "$BASE_SHA" -- ; git ls-files --others --exclude-standard; } 2>/dev/null | sort -u
}

run_check() { # $1 name, $2 command
  local name="$1" cmd="$2" log="$STATE/last-$1.log"
  if [ -z "$cmd" ]; then echo "skip" ; return; fi
  if bash -c "$cmd" >"$log" 2>&1; then echo "pass"; else echo "fail"; fi
}

syntax_check() {
  local log="$STATE/last-syntax.log" rc=0 f
  : >"$log"
  while IFS= read -r f; do
    [ -f "$f" ] || continue
    case "$f" in
      vendor/*|node_modules/*) continue ;;
      *.php)  php -l "$f" >>"$log" 2>&1 || rc=1 ;;
      *.go)   out=$(gofmt -l "$f" 2>&1); [ -n "$out" ] && { echo "gofmt: $out" >>"$log"; rc=1; } ;;
      *.js|*.mjs|*.cjs) node --check "$f" >>"$log" 2>&1 || rc=1 ;;
      *.py)   python3 -m py_compile "$f" >>"$log" 2>&1 || rc=1 ;;
      *.json) jq empty "$f" >>"$log" 2>&1 || rc=1 ;;
    esac
  done < <(changed_files)
  [ $rc -eq 0 ] && echo "pass" || echo "fail"
}

secrets_check() {
  local log="$STATE/last-secrets.log" f
  local pattern='AKIA[0-9A-Z]{16}|-----BEGIN [A-Z ]*PRIVATE KEY-----|(password|passwd|secret|api[_-]?key|token)["'"'"']?[[:space:]]*[:=>]+[[:space:]]*["'"'"'][^"'"'"'$[:space:]]{8,}["'"'"']|xox[baprs]-[0-9A-Za-z-]{10,}|ghp_[0-9A-Za-z]{36}|sk-[A-Za-z0-9]{32,}'
  {
    # baris yang ditambah pada file tracked
    [ -n "$BASE_SHA" ] && git diff "$BASE_SHA" -U0 -- 2>/dev/null | grep -E '^\+[^+]' | grep -E "$pattern"
    # file baru (untracked), lewati biner
    while IFS= read -r -d '' f; do
      [ -f "$f" ] && grep -I -n -H -E "$pattern" -- "$f" 2>/dev/null
    done < <(git ls-files --others --exclude-standard -z)
  } >"$log"
  [ -s "$log" ] && echo "fail" || echo "pass"
}

CHECKS="test lint typecheck"
if [ "$MODE" = "--baseline" ]; then
  git rev-parse HEAD >"$STATE/base_sha" 2>/dev/null
  : >"$STATE/baseline.env"
  echo "Baseline untuk $ROOT  (stack: $STACK)"
  for c in $CHECKS; do
    var="$(echo "$c" | tr a-z A-Z)_CMD"; st=$(run_check "$c" "${!var}")
    echo "BASELINE_$c=$st" >>"$STATE/baseline.env"
    printf "  %-10s %s\n" "$c" "$st"
  done
  exit 0
fi

# shellcheck disable=SC1091
[ -f "$STATE/baseline.env" ] && source "$STATE/baseline.env"

BLOCK=0
REPORT=""
add() { REPORT+="$1"$'\n'; }
tail_log() { add "----- $1 (40 baris terakhir) -----"; add "$(tail -n 40 "$STATE/last-$1.log" 2>/dev/null)"; }

st=$(syntax_check);  add "syntax     $st"; [ "$st" = fail ] && { BLOCK=1; tail_log syntax; }
st=$(secrets_check); add "secrets    $st"; [ "$st" = fail ] && { BLOCK=1; tail_log secrets; }
for c in $CHECKS; do
  var="$(echo "$c" | tr a-z A-Z)_CMD"; st=$(run_check "$c" "${!var}")
  base_var="BASELINE_$c"; base="${!base_var:-pass}"
  if [ "$st" = fail ] && [ "$base" = fail ]; then
    add "$(printf '%-10s %s' "$c" 'fail (pre-existing di baseline, tidak memblok — pastikan tidak bertambah)')"
  else
    add "$(printf '%-10s %s' "$c" "$st")"
    [ "$st" = fail ] && { BLOCK=1; tail_log "$c"; }
  fi
done

echo "QUALITY GATE — $ROOT (stack: $STACK)"
echo "$REPORT"
if [ $BLOCK -eq 0 ]; then echo "RESULT: PASS"; echo pass >"$STATE/last_gate"; exit 0
else echo "RESULT: FAIL"; echo fail >"$STATE/last_gate"; exit 1
fi
