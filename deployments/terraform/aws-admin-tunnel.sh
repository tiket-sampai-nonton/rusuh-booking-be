#!/usr/bin/env bash
# Tunnel SSM ke Booking di server core, untuk rute /internal/v1/admin/* yang sengaja tidak diekspos lewat ALB/Cloudflare:
#   AWS_PROFILE=rusuh ENV_NAME=prod AWS_REGION=ap-southeast-2 bash aws-admin-tunnel.sh
# Hasil: http://localhost:18082 -> booking-api (ubah port lokal dengan LOCAL_PORT). Ctrl+C untuk berhenti.
# Butuh session-manager-plugin (brew install --cask session-manager-plugin). Tidak ada port yang dibuka ke internet.
set -euo pipefail
: "${ENV_NAME:?}" "${AWS_REGION:?}"
export AWS_REGION AWS_DEFAULT_REGION="$AWS_REGION"
port="${LOCAL_PORT:-18082}"
command -v session-manager-plugin >/dev/null || { echo "pasang dulu: brew install --cask session-manager-plugin" >&2; exit 1; }

id="$(aws ec2 describe-instances --filters "Name=tag:Name,Values=rusuh-$ENV_NAME-core" Name=instance-state-name,Values=running \
  --query 'Reservations[0].Instances[0].InstanceId' --output text)"
[ -n "$id" ] && [ "$id" != None ] || { echo "server core tidak berjalan (jalankan aws-power.sh on)" >&2; exit 1; }

echo "tunnel ke $id: admin Booking di http://localhost:$port (Ctrl+C untuk berhenti)"
exec aws ssm start-session --target "$id" --document-name AWS-StartPortForwardingSession \
  --parameters "{\"portNumber\":[\"8082\"],\"localPortNumber\":[\"$port\"]}"
