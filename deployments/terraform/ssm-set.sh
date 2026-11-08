#!/usr/bin/env bash
# Mengubah satu atau beberapa KEY=VALUE di parameter env SSM tanpa menyentuh nilai lain (jangan menjalankan ulang seed-ssm.sh):
#   ENV_NAME=prod AWS_REGION=ap-southeast-2 bash ssm-set.sh payment \
#     PAYMENT_SUCCESS_RETURN_URL=https://app.contoh.id/payment/success PAYMENT_FAILED_RETURN_URL=https://app.contoh.id/payment/failed
# Hanya nama key yang dicetak, bukan nilainya. Perubahan berlaku saat container dibuat ulang (deploy-aws.sh dengan tag yang sama).
set -euo pipefail
: "${ENV_NAME:?}" "${AWS_REGION:?}"
export AWS_REGION AWS_DEFAULT_REGION="$AWS_REGION"
svc="${1:?pakai: ssm-set.sh <service> KEY=VALUE ...}"
shift
[ $# -gt 0 ] || { echo "beri minimal satu KEY=VALUE" >&2; exit 2; }
param="/rusuh-$ENV_NAME/$svc.env"

tmp="$(mktemp)"
trap 'rm -f "$tmp" "$tmp.new"' EXIT
aws ssm get-parameter --name "$param" --with-decryption --query Parameter.Value --output text > "$tmp"

keys=""
for kv in "$@"; do
  k="${kv%%=*}"
  v="${kv#*=}"
  grep -q "^$k=" "$tmp" || { echo "$k tidak ada di $param; tidak ada yang diubah" >&2; exit 1; }
  # awk, bukan sed: nilai boleh memuat / & dan karakter lain tanpa perlu di-escape.
  K="$k" V="$v" awk 'BEGIN{FS=OFS="="} $1==ENVIRON["K"]{print ENVIRON["K"] "=" ENVIRON["V"]; next} {print}' "$tmp" > "$tmp.new"
  mv "$tmp.new" "$tmp"
  keys="$keys $k"
done
aws ssm put-parameter --type SecureString --overwrite --name "$param" --value "file://$tmp" >/dev/null
echo "diperbarui $param:$keys"
