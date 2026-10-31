variable "name" { type = string }
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
  notification {
    comparison_operator        = "GREATER_THAN"
    threshold                  = 100
    threshold_type             = "PERCENTAGE"
    notification_type          = "ACTUAL"
    subscriber_email_addresses = [var.alert_email]
  }
}
