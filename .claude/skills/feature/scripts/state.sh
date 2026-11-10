#!/usr/bin/env bash
# State workflow /feature, disimpan di <git-dir>/feature-workflow/ (tidak pernah ikut ter-commit).
#   state.sh init "<deskripsi>"   mulai workflow baru (phase=planning, attempts=0)
#   state.sh phase <nama>         planning | coding | review | fix | paused | done | escalated
#   state.sh get <key>            baca nilai (phase, attempts, max_attempts, feature, branch, base_sha)
#   state.sh status               ringkasan
#   state.sh dir                  path folder state
#   state.sh reset                hapus state (setelah selesai / batal)
set -uo pipefail

GIT_DIR="$(git rev-parse --absolute-git-dir 2>/dev/null)" || { echo "Bukan git repo." >&2; exit 1; }
STATE="$GIT_DIR/feature-workflow"
mkdir -p "$STATE"

cmd="${1:-status}"; shift || true
case "$cmd" in
  init)
    rm -f "$STATE"/last-*.log "$STATE/last_gate" "$STATE/baseline.env" "$STATE/commands.env.auto"
    echo "${1:-}" >"$STATE/feature"
    echo planning >"$STATE/phase"
    echo 0 >"$STATE/attempts"
    echo "${FEATURE_MAX_ATTEMPTS:-3}" >"$STATE/max_attempts"
    git rev-parse --abbrev-ref HEAD >"$STATE/branch" 2>/dev/null
    git rev-parse HEAD >"$STATE/base_sha" 2>/dev/null
    echo "State: $STATE"
    ;;
  phase)  echo "$1" >"$STATE/phase"; [ "$1" = coding ] && echo 0 >"$STATE/attempts"; echo "phase=$1" ;;
  get)    cat "$STATE/$1" 2>/dev/null ;;
  dir)    echo "$STATE" ;;
  reset)  rm -rf "$STATE"; echo "State dihapus." ;;
  status)
    for k in feature phase attempts max_attempts branch base_sha last_gate; do
      printf "%-13s %s\n" "$k" "$(cat "$STATE/$k" 2>/dev/null || echo -)"
    done ;;
  *) echo "Perintah tidak dikenal: $cmd" >&2; exit 1 ;;
esac
