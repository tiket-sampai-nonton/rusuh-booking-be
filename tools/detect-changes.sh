#!/usr/bin/env bash
# Menentukan modul/service yang perlu diproses CI.
# Env: EVENT (push|pull_request|workflow_dispatch), PR_BASE, PUSH_BEFORE.
# Output (ke $GITHUB_OUTPUT, atau stdout bila dijalankan lokal): modules, services, any, any_services.
set -euo pipefail

SERVICES=(queue booking payment notification)
ZERO=0000000000000000000000000000000000000000
OUT="${GITHUB_OUTPUT:-/dev/stdout}"

base=""
case "${EVENT:-}" in
  pull_request) base="${PR_BASE:-}" ;;
  push) base="${PUSH_BEFORE:-}" ;;
esac

all=1
files=""
if [ -n "$base" ] && [ "$base" != "$ZERO" ] && git cat-file -e "${base}^{commit}" 2>/dev/null; then
  files="$(git diff --name-only "$base" HEAD)"
  all=0
fi

# Perubahan pada bagian bersama memicu semua service.
if [ "$all" = 0 ] && printf '%s\n' "$files" | grep -Eq '^(pkg/|go\.work|go\.work\.sum|\.golangci\.yml|\.github/workflows/ci\.yml|tools/|sonar-project\.properties|Makefile$)'; then
  all=1
fi

selected=()
for s in "${SERVICES[@]}"; do
  if [ "$all" = 1 ] || printf '%s\n' "$files" | grep -q "^services/${s}/"; then
    selected+=("$s")
  fi
done

obj() { printf '{"name":"%s","path":"%s"}' "$1" "$2"; }

services="["
sep=""
for s in ${selected[@]+"${selected[@]}"}; do
  services+="${sep}$(obj "$s" "services/$s")"
  sep=","
done
services+="]"

modules="["
sep=""
if [ "$all" = 1 ]; then
  modules+="$(obj pkg pkg)"
  sep=","
fi
for s in ${selected[@]+"${selected[@]}"}; do
  modules+="${sep}$(obj "$s" "services/$s")"
  sep=","
done
modules+="]"

any=false
[ "$modules" != "[]" ] && any=true
any_services=false
[ "$services" != "[]" ] && any_services=true

{
  echo "modules=$modules"
  echo "services=$services"
  echo "any=$any"
  echo "any_services=$any_services"
} >> "$OUT"

echo "all=$all modules=$modules" >&2
