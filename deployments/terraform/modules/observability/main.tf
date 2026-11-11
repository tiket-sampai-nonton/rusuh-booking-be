variable "name" { type = string }
variable "region" { type = string }
variable "alert_email" { type = string }
variable "budget_limits_usd" { type = list(number) }
variable "alb_arn_suffix" { type = string }
variable "target_group_arn_suffixes" { type = map(string) }

# Tidak dienkripsi KMS: alias/aws/sns tidak mengizinkan CloudWatch/Budgets mem-publish; isi pesan hanya metadata alarm.
#trivy:ignore:AVD-AWS-0136
#trivy:ignore:AVD-AWS-0095
resource "aws_sns_topic" "alerts" {
  name_prefix = "${var.name}-alerts-"
}

resource "aws_sns_topic_subscription" "email" {
  topic_arn = aws_sns_topic.alerts.arn
  protocol  = "email"
  endpoint  = var.alert_email
}

resource "aws_cloudwatch_metric_alarm" "alb_5xx" {
  alarm_name          = "${var.name}-alb-5xx"
  namespace           = "AWS/ApplicationELB"
  metric_name         = "HTTPCode_Target_5XX_Count"
  dimensions          = { LoadBalancer = var.alb_arn_suffix }
  statistic           = "Sum"
  period              = 300
  evaluation_periods  = 1
  threshold           = 10
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}

resource "aws_cloudwatch_metric_alarm" "unhealthy" {
  for_each            = var.target_group_arn_suffixes
  alarm_name          = "${var.name}-${each.key}-unhealthy"
  namespace           = "AWS/ApplicationELB"
  metric_name         = "UnHealthyHostCount"
  dimensions          = { LoadBalancer = var.alb_arn_suffix, TargetGroup = each.value }
  statistic           = "Maximum"
  period              = 60
  evaluation_periods  = 3
  threshold           = 0
  comparison_operator = "GreaterThanThreshold"
  treat_missing_data  = "notBreaching"
  alarm_actions       = [aws_sns_topic.alerts.arn]
}

resource "aws_budgets_budget" "monthly" {
  for_each     = toset([for l in var.budget_limits_usd : tostring(l)])
  name         = "${var.name}-${each.value}usd"
  budget_type  = "COST"
  limit_amount = each.value
  limit_unit   = "USD"
  time_unit    = "MONTHLY"
  # Tanpa ini kredit AWS dikurangkan dari biaya, sehingga selama masih ada kredit angkanya 0 dan alert tidak pernah terkirim.
  cost_types {
    include_credit = false
  }
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.alert_email]
  }
}

# ---------------------------------------------------------------- NFR-09: antrean, konversi, expired per event
# Sumber: CloudWatch Agent di instance app (EMF, lihat compute/user_data.sh.tftpl). Rasio dihitung di Booking (Go), sama
# dengan dashboard Grafana dev. Setiap instance melaporkan angka yang sama, jadi query memakai MAX/MIN, bukan SUM.
locals {
  ns = "RBT/${var.name}"
  business_widgets = [
    ["Antrean menunggu per event", "SELECT MAX(rbt_queue_waiting_count) FROM SCHEMA(\"${local.ns}\", event_id) GROUP BY event_id"],
    ["Slot aktif per event", "SELECT MAX(rbt_queue_active_count) FROM SCHEMA(\"${local.ns}\", event_id) GROUP BY event_id"],
    ["Konversi (%) = PAID / admission", "SELECT MAX(rbt_booking_conversion_percent) FROM SCHEMA(\"${local.ns}\", event_id) GROUP BY event_id"],
    ["Booking expired (%)", "SELECT MAX(rbt_booking_expired_percent) FROM SCHEMA(\"${local.ns}\", event_id) GROUP BY event_id"],
    ["Missed turn (%)", "SELECT MAX(rbt_booking_missed_turn_percent) FROM SCHEMA(\"${local.ns}\", event_id) GROUP BY event_id"],
    ["Sumber metrik terbaca (1 = OK)", "SELECT MIN(rbt_booking_funnel_up) FROM SCHEMA(\"${local.ns}\", service) GROUP BY service"],
  ]
  # Ambang awal (tebakan PoC, sama dengan alert Grafana dev): sesuaikan setelah ada data uji beban atau penjualan nyata.
  # Berbeda dari Grafana, alarm CloudWatch tidak bisa menyaring event dengan volume kecil, jadi butuh 5 menit berturut-turut.
  business_alarms = {
    funnel-down = {
      query    = "SELECT MIN(rbt_booking_funnel_up) FROM SCHEMA(\"${local.ns}\", service)"
      operator = "LessThanThreshold", threshold = 1, periods = 3
      summary  = "Funnel Booking gagal dibaca dari PostgreSQL (rbt_booking_funnel_up = 0)."
    }
    queue-depth-down = {
      query    = "SELECT MIN(rbt_queue_depth_up) FROM SCHEMA(\"${local.ns}\", service)"
      operator = "LessThanThreshold", threshold = 1, periods = 3
      summary  = "Kedalaman antrean gagal dibaca dari Valkey (rbt_queue_depth_up = 0)."
    }
    missed-turn-high = {
      query    = "SELECT MAX(rbt_booking_missed_turn_percent) FROM SCHEMA(\"${local.ns}\", event_id)"
      operator = "GreaterThanThreshold", threshold = 30, periods = 5
      summary  = "Lebih dari 30% admission pada salah satu event berakhir UNCLAIMED."
    }
    booking-expired-high = {
      query    = "SELECT MAX(rbt_booking_expired_percent) FROM SCHEMA(\"${local.ns}\", event_id)"
      operator = "GreaterThanThreshold", threshold = 50, periods = 5
      summary  = "Lebih dari 50% booking pada salah satu event berakhir EXPIRED."
    }
  }
}

resource "aws_cloudwatch_dashboard" "business" {
  dashboard_name = "${var.name}-bisnis"
  dashboard_body = jsonencode({
    widgets = [for i, w in local.business_widgets : {
      type   = "metric"
      x      = (i % 2) * 12
      y      = floor(i / 2) * 6
      width  = 12
      height = 6
      properties = {
        title   = w[0]
        region  = var.region
        view    = "timeSeries"
        stat    = "Maximum"
        period  = 60
        metrics = [[{ expression = w[1], id = "q${i}", period = 60 }]]
      }
    }]
  })
}

resource "aws_cloudwatch_metric_alarm" "business" {
  for_each            = local.business_alarms
  alarm_name          = "${var.name}-${each.key}"
  alarm_description   = each.value.summary
  comparison_operator = each.value.operator
  threshold           = each.value.threshold
  evaluation_periods  = each.value.periods
  datapoints_to_alarm = each.value.periods
  # Tanpa data (instance dimatikan aws-power.sh) bukan insiden; API yang mati sudah tertangkap alarm unhealthy host.
  treat_missing_data = "notBreaching"
  alarm_actions      = [aws_sns_topic.alerts.arn]
  ok_actions         = [aws_sns_topic.alerts.arn]

  metric_query {
    id          = "q"
    expression  = each.value.query
    period      = 60
    return_data = true
  }
}
