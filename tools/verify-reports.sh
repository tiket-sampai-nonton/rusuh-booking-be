#!/usr/bin/env bash
# Gerbang hasil scan. Gagal bila ada temuan yang melewati ambang.
# Pakai:
#   tools/verify-reports.sh quality <dir-laporan>   # dir berisi quality-<modul>/ dan quality-secrets/
#   tools/verify-reports.sh image   <dir-laporan>   # dir berisi scan-<service>/trivy-<service>.json
# Env (image): IMAGE_FAIL_SEVERITY, daftar dipisah koma, default CRITICAL.
set -uo pipefail

mode="${1:?quality|image}"
dir="${2:?direktori laporan}"
fail=0

bad() { echo "::error::$*"; fail=1; }

# count <file> <label> <filter-jq>  -> gagal bila file hilang/tidak valid atau hasil > 0
count() {
  local f="$1" label="$2" q="$3" n
  if [ ! -s "$f" ]; then bad "$label: laporan tidak ada ($f)"; return; fi
  if ! n=$(jq --arg sev "${IMAGE_FAIL_SEVERITY:-CRITICAL}" "$q" "$f" 2>/dev/null); then
    bad "$label: laporan tidak valid ($f)"; return
  fi
  if [ "$n" -gt 0 ]; then bad "$label: $n temuan"; else echo "ok  $label"; fi
}

found=0
case "$mode" in
  quality)
    for d in "$dir"/quality-*; do
      [ -d "$d" ] || continue
      found=1
      name="${d##*/quality-}"
      if [ "$name" = "secrets" ]; then
        count "$d/gitleaks.json" "secrets" 'length'
        continue
      fi
      count "$d/gosec.json" "$name gosec (HIGH)" '[.Issues[]? | select(.severity == "HIGH")] | length'
      count "$d/govulncheck.sarif" "$name govulncheck" '[.runs[]?.results[]?] | length'
      if [ "$name" != "pkg" ]; then
        count "$d/hadolint.json" "$name hadolint (error)" '[.[] | select(.level == "error")] | length'
      fi
    done
    ;;
  image)
    for d in "$dir"/scan-*; do
      [ -d "$d" ] || continue
      found=1
      name="${d##*/scan-}"
      count "$d/trivy-${name}.json" "$name image (${IMAGE_FAIL_SEVERITY:-CRITICAL})" \
        '[.Results[]?.Vulnerabilities[]? | select(.Severity as $s | ($sev | split(",") | index($s)) != null)] | length'
    done
    ;;
  *)
    echo "mode tidak dikenal: $mode" >&2
    exit 2
    ;;
esac

if [ "$found" = 0 ]; then
  bad "tidak ada laporan di $dir"
fi
exit "$fail"
