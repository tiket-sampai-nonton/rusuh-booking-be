#!/usr/bin/env bash
# Menyalakan atau mematikan server AWS di antara sesi demo, tanpa menghapus data:
#   ENV_NAME=prod AWS_REGION=ap-southeast-2 API_DOMAIN=api.rusuhbooking.my.id bash aws-power.sh off
#   ENV_NAME=prod AWS_REGION=ap-southeast-2 API_DOMAIN=api.rusuhbooking.my.id bash aws-power.sh on
# shortcut: on mengembalikan tiap ASG ke 1 instance, ubah bila desired di tfvars bukan 1.
set -euo pipefail
: "${ENV_NAME:?}" "${AWS_REGION:?}" "${API_DOMAIN:?}"
export AWS_REGION AWS_DEFAULT_REGION="$AWS_REGION"
name="rusuh-$ENV_NAME"

data_id="$(aws ec2 describe-instances --filters "Name=tag:Name,Values=$name-data" Name=instance-state-name,Values=pending,running,stopping,stopped \
  --query 'Reservations[0].Instances[0].InstanceId' --output text)"
[ -n "$data_id" ] && [ "$data_id" != None ] || { echo "host data $name-data tidak ditemukan" >&2; exit 1; }
asgs="$(aws autoscaling describe-auto-scaling-groups \
  --query "AutoScalingGroups[?starts_with(AutoScalingGroupName, '$name-')].AutoScalingGroupName" --output text)"
[ -n "$asgs" ] || { echo "ASG $name-* tidak ditemukan" >&2; exit 1; }

case "${1:-}" in
  off)
    for asg in $asgs; do aws autoscaling update-auto-scaling-group --auto-scaling-group-name "$asg" --min-size 0 --desired-capacity 0; done
    # App berhenti dulu supaya tidak menulis saat database dimatikan.
    for _ in $(seq 1 60); do
      left="$(aws autoscaling describe-auto-scaling-groups \
        --query "AutoScalingGroups[?starts_with(AutoScalingGroupName, '$name-')].Instances[].InstanceId" --output text)"
      [ -z "$left" ] && break
      sleep 10
    done
    [ -z "$left" ] || { echo "instance ASG belum habis: $left; host data tidak dimatikan" >&2; exit 1; }
    aws ec2 stop-instances --instance-ids "$data_id" >/dev/null
    aws ec2 wait instance-stopped --instance-ids "$data_id"
    # Snapshot saat host berhenti supaya konsisten; simpan 3 terakhir (DLM tidak tersedia di Free plan).
    vol="$(aws ec2 describe-volumes --filters "Name=tag:Name,Values=$name-data" --query 'Volumes[0].VolumeId' --output text)"
    aws ec2 create-snapshot --volume-id "$vol" --description "$name aws-power off" \
      --tag-specifications "ResourceType=snapshot,Tags=[{Key=Name,Value=$name-power-off}]" >/dev/null
    for old in $(aws ec2 describe-snapshots --owner-ids self --filters "Name=tag:Name,Values=$name-power-off" \
      --query 'sort_by(Snapshots,&StartTime)[:-3].SnapshotId' --output text); do
      aws ec2 delete-snapshot --snapshot-id "$old"
    done
    echo "mati: ASG 0 instance, host data berhenti, snapshot dibuat. Volume dan ALB tetap ada."
    ;;
  on)
    aws ec2 start-instances --instance-ids "$data_id" >/dev/null
    aws ec2 wait instance-running --instance-ids "$data_id"
    for asg in $asgs; do aws autoscaling update-auto-scaling-group --auto-scaling-group-name "$asg" --min-size 1 --desired-capacity 1; done
    echo "menunggu semua service siap (biasanya 5-10 menit)"
    SMOKE_TRIES=60 SMOKE_SLEEP=15 bash "$(dirname "$0")/smoke-test.sh" && { echo "nyala"; exit 0; }
    echo "API belum siap" >&2
    exit 1
    ;;
  *) echo "pakai: aws-power.sh on|off" >&2; exit 2 ;;
esac
