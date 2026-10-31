#!/usr/bin/env bash
# Memasang timer backup (02:30) dan housekeeping (03:00) di server. Jalankan sekali sebagai admin (sudo):
#   sudo bash /opt/rusuh/repo/deployments/dev/install-timers.sh
set -euo pipefail
[ "$(id -u)" = 0 ] || { echo "jalankan dengan sudo" >&2; exit 1; }
here="$(cd "$(dirname "$0")" && pwd)"
install -m 0644 "$here"/systemd/rusuh-*.service "$here"/systemd/rusuh-*.timer /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now rusuh-backup.timer rusuh-housekeeping.timer
systemctl list-timers 'rusuh-*' --no-pager
