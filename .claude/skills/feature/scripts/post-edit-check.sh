#!/usr/bin/env bash
# PostToolUse hook (Edit|Write|MultiEdit): cek sintaks file yang baru diubah, cepat.
# Hanya aktif saat workflow /feature sedang berjalan. Gagal -> exit 2, stderr dikirim ke Claude.
set -uo pipefail

INPUT="$(cat)"
FILE="$(echo "$INPUT" | jq -r '.tool_input.file_path // empty' 2>/dev/null)"
[ -n "$FILE" ] && [ -f "$FILE" ] || exit 0
cd "$(dirname "$FILE")" 2>/dev/null || exit 0
GIT_DIR="$(git rev-parse --absolute-git-dir 2>/dev/null)" || exit 0
PHASE="$(cat "$GIT_DIR/feature-workflow/phase" 2>/dev/null || echo none)"
case "$PHASE" in coding|review|fix) ;; *) exit 0 ;; esac

case "$FILE" in
  *.php)            OUT="$(php -l "$FILE" 2>&1)" || { echo "Syntax error PHP: $OUT" >&2; exit 2; } ;;
  *.go)             OUT="$(gofmt -l -e "$FILE" 2>&1)"; [ -n "$OUT" ] && { echo "gofmt/syntax: $OUT — jalankan gofmt -w." >&2; exit 2; } ;;
  *.js|*.mjs|*.cjs) OUT="$(node --check "$FILE" 2>&1)" || { echo "Syntax error JS: $OUT" >&2; exit 2; } ;;
  *.py)             OUT="$(python3 -m py_compile "$FILE" 2>&1)" || { echo "Syntax error Python: $OUT" >&2; exit 2; } ;;
  *.json)           OUT="$(jq empty "$FILE" 2>&1)" || { echo "JSON tidak valid: $OUT" >&2; exit 2; } ;;
esac
exit 0
