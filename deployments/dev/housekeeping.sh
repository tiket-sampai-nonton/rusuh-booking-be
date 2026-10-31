#!/usr/bin/env bash
# Pembersihan harian VPS dev (disk 80 GB). Dijalankan systemd timer jam 03:00 sebagai user deploy.
# Aturan keselamatan: TIDAK PERNAH menghapus volume (tidak ada `docker volume prune` / `system prune --volumes`).
set -uo pipefail
RUSUH_DIR="${RUSUH_DIR:-/opt/rusuh}"
log() { echo "$(date -Is) housekeeping: $*"; }

before="$(df --output=used -B1M / | tail -1 | tr -d ' ')"
log "mulai; disk terpakai ${before} MB"

docker image prune -af --filter "until=72h" || log "image prune gagal"
docker builder prune -af --filter "until=72h" || log "builder prune gagal"
docker container prune -f --filter "until=72h" || log "container prune gagal"

# dump backup lokal > 7 hari
find "$RUSUH_DIR/backups" -type f \( -name '*.sql.gz' -o -name '*.globals.sql' \) -mtime +7 -delete 2>/dev/null || true
# journal sistem (perlu hak; dilewati bila bukan root dan tanpa sudo tanpa password)
(sudo -n journalctl --vacuum-time=7d >/dev/null 2>&1 && sudo -n apt-get clean >/dev/null 2>&1) || log "journal/apt dilewati (tanpa sudo)"

after="$(df --output=used -B1M / | tail -1 | tr -d ' ')"
pct="$(df --output=pcent / | tail -1 | tr -dc '0-9')"
log "selesai; disk terpakai ${after} MB (${pct}%), dibebaskan $((before - after)) MB"
if [ "$pct" -ge 80 ]; then log "PERINGATAN: disk ${pct}% penuh (ambang 80%)"; fi
