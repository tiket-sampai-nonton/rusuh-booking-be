#!/usr/bin/env bash
# Rilis atau rollback image ke AWS, dipakai job deployment-prod dan operator:
#   ENV_NAME=prod IMAGE_TAG=git-<sha> AWS_REGION=ap-southeast-2 API_DOMAIN=api.rusuhbooking.my.id bash deploy-aws.sh
# Urutan: pastikan image ada -> set parameter image_tag -> notification di host stateful -> instance refresh ASG -> smoke test.
# Migrasi berjalan otomatis saat container start. Rollback image tidak memundurkan skema database.
set -euo pipefail
: "${ENV_NAME:?}" "${IMAGE_TAG:?}" "${AWS_REGION:?}" "${API_DOMAIN:?}"
export AWS_REGION AWS_DEFAULT_REGION="$AWS_REGION"
name="rusuh-$ENV_NAME"

for s in queue booking payment notification user; do
  aws ecr describe-images --repository-name "$name/$s" --image-ids imageTag="$IMAGE_TAG" >/dev/null
done
aws ssm put-parameter --name "/$name/image_tag" --type String --value "$IMAGE_TAG" --overwrite >/dev/null
echo "image_tag = $IMAGE_TAG"

data_id="$(aws ec2 describe-instances --filters "Name=tag:Name,Values=$name-data" Name=instance-state-name,Values=running \
  --query 'Reservations[0].Instances[0].InstanceId' --output text)"
cmd="$(aws ssm send-command --instance-ids "$data_id" --document-name "$name-deploy-notification" --query Command.CommandId --output text)"
status=Pending
for _ in $(seq 1 90); do
  sleep 10
  status="$(aws ssm get-command-invocation --command-id "$cmd" --instance-id "$data_id" --query Status --output text 2>/dev/null || echo Pending)"
  case "$status" in Success) break ;; Failed|Cancelled|TimedOut) break ;; esac
done
[ "$status" = Success ] || { echo "deploy notification gagal: $status (command $cmd)" >&2; exit 1; }
echo "notification selesai"

asgs="$(aws autoscaling describe-auto-scaling-groups \
  --query "AutoScalingGroups[?starts_with(AutoScalingGroupName, '$name-')].AutoScalingGroupName" --output text)"
[ -n "$asgs" ] || { echo "ASG $name-* tidak ditemukan" >&2; exit 1; }
for asg in $asgs; do
  aws autoscaling start-instance-refresh --auto-scaling-group-name "$asg" \
    --preferences '{"MinHealthyPercentage":100,"MaxHealthyPercentage":200,"InstanceWarmup":180}' >/dev/null
done
for asg in $asgs; do
  status=Pending
  for _ in $(seq 1 120); do
    sleep 15
    status="$(aws autoscaling describe-instance-refreshes --auto-scaling-group-name "$asg" --max-records 1 --query 'InstanceRefreshes[0].Status' --output text)"
    case "$status" in Successful|Failed|Cancelled|RollbackSuccessful|RollbackFailed) break ;; esac
  done
  [ "$status" = Successful ] || { echo "instance refresh $asg: $status" >&2; exit 1; }
  echo "$asg selesai"
done

# Status EC2 sehat belum berarti API sehat (health check ASG tipe EC2), jadi cek lewat jalur publik.
for _ in $(seq 1 18); do
  code="$(curl -s -m 10 -o /dev/null -w '%{http_code}' "https://$API_DOMAIN/api/v1/events" || true)"
  [ "$code" = 200 ] && { echo "smoke test lulus (GET /api/v1/events = 200)"; exit 0; }
  sleep 10
done
echo "smoke test gagal: HTTP $code" >&2
exit 1
