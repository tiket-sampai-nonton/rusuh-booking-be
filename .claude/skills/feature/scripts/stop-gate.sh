#!/usr/bin/env bash
# Stop hook: selama workflow /feature berada di fase coding/review/fix, Claude tidak boleh
# berhenti sebelum quality gate PASS. Tiap blok = satu putaran auto-fix (FASE 4),
# dibatasi max_attempts (default 3). Lewat batas -> phase=escalated, Claude boleh berhenti
# dan harus melapor ke user.
set -uo pipefail

INPUT="$(cat)"
CWD="$(echo "$INPUT" | jq -r '.cwd // empty' 2>/dev/null)"
[ -n "$CWD" ] && cd "$CWD" 2>/dev/null
GIT_DIR="$(git rev-parse --absolute-git-dir 2>/dev/null)" || exit 0
STATE="$GIT_DIR/feature-workflow"
PHASE="$(cat "$STATE/phase" 2>/dev/null || echo none)"

case "$PHASE" in coding|review|fix) ;; *) exit 0 ;; esac

SCRIPTS="$(cd "$(dirname "$0")" && pwd)"
OUT="$("$SCRIPTS/quality-gate.sh" 2>&1)"; RC=$?
OUT="$(echo "$OUT" | tail -c 6000)"

if [ $RC -eq 0 ]; then
  exit 0
fi

ATTEMPTS=$(( $(cat "$STATE/attempts" 2>/dev/null || echo 0) + 1 ))
MAX="$(cat "$STATE/max_attempts" 2>/dev/null || echo 3)"
echo "$ATTEMPTS" >"$STATE/attempts"

if [ "$ATTEMPTS" -gt "$MAX" ]; then
  echo escalated >"$STATE/phase"
  jq -n --arg m "Workflow /feature: quality gate masih FAIL setelah $MAX putaran auto-fix. Phase=escalated — butuh keputusan user." \
    '{systemMessage: $m}'
  exit 0
fi

echo fix >"$STATE/phase"
REASON="FASE 4 — AUTO-FIX LOOP (putaran $ATTEMPTS/$MAX). Quality gate FAIL. Perbaiki akar masalah \
(pakai superpowers:systematic-debugging untuk tes yang gagal), JANGAN melemahkan/menghapus tes atau menonaktifkan lint. \
Setelah perbaikan, ulangi review singkat (FASE 3) pada bagian yang diubah.

$OUT"
jq -n --arg r "$REASON" '{decision: "block", reason: $r}'
exit 0
