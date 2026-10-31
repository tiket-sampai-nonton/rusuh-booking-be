#!/usr/bin/env bash
# Backup harian: dump database rbt + globals (role) ke $RUSUH_DIR/backups, gzip. Dijalankan timer jam 02:30.
# Salinan ke luar VPS dilakukan terpisah (laptop/object storage); rotasi lokal 7 hari ada di housekeeping.sh.
set -euo pipefail
RUSUH_DIR="${RUSUH_DIR:-/opt/rusuh}"
cd "$(dirname "$0")"
stamp="$(date +%Y%m%d-%H%M%S)"
out="$RUSUH_DIR/backups"
dc() { docker compose -f docker-compose.dev.yaml "$@"; }
export RUSUH_UID="$(id -u)" RUSUH_GID="$(id -g)" IMAGE_PREFIX="${IMAGE_PREFIX:-x}" IMAGE_TAG="${IMAGE_TAG:-x}"

umask 077
dc exec -T postgres sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" --no-owner' | gzip > "$out/rbt-$stamp.sql.gz"
dc exec -T postgres sh -c 'pg_dumpall -U "$POSTGRES_USER" --globals-only' > "$out/rbt-$stamp.globals.sql"
test -s "$out/rbt-$stamp.sql.gz"
echo "$(date -Is) backup: $out/rbt-$stamp.sql.gz ($(du -h "$out/rbt-$stamp.sql.gz" | cut -f1))"
