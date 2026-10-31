variable "name" { type = string }
variable "region" { type = string }
variable "vpc_id" { type = string }
variable "subnet_ids" { type = list(string) }
variable "alb_sg_id" { type = string }
variable "app_sg_id" { type = string }
variable "instance_profile_name" { type = string }
variable "registry" { type = string }
variable "image_tag" { type = string }
variable "log_group" { type = string }
variable "certificate_arn" { type = string }
variable "data_host_instance_id" { type = string }
variable "data_host_ip" { type = string }
variable "services" {
  type = map(object({
    port  = number
    paths = list(string)
    tier  = string
  }))
}
variable "asgs" {
  description = "ASG per tier (queue, core)."
  type        = map(object({ min = number, max = number, desired = number, instance_type = string }))
}

locals {
  priorities = { for i, k in sort(keys(var.services)) : k => 100 + i * 10 }
  # Worker berjalan di instance yang sama dengan api-nya (HLD: satu verb per container).
  has_worker = { for k, _ in var.services : k => contains(["queue", "booking", "payment"], k) }
  ports      = { for k, s in var.services : k => s.port }
}

data "aws_ssm_parameter" "ami" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64"
}

# ---------------------------------------------------------------- ALB
# Internet-facing sesuai HLD §4.5; akses dibatasi security group ke CIDR yang diizinkan.
#trivy:ignore:AVD-AWS-0053
resource "aws_lb" "this" {
  name_prefix                = "rb-"
  load_balancer_type         = "application"
  subnets                    = var.subnet_ids
  security_groups            = [var.alb_sg_id]
  drop_invalid_header_fields = true
  idle_timeout               = 60
}

resource "aws_lb_target_group" "svc" {
  for_each    = var.services
  name_prefix = substr(each.key, 0, 6)
  vpc_id      = var.vpc_id
  port        = each.value.port
  protocol    = "HTTP"
  target_type = "instance"
  health_check {
    path                = "/health"
    matcher             = "200"
    interval            = 15
    healthy_threshold   = 2
    unhealthy_threshold = 3
    timeout             = 5
  }
  deregistration_delay = 30
  lifecycle { create_before_destroy = true }
}

# Notification berjalan di host stateful, bukan di ASG.
resource "aws_lb_target_group_attachment" "data_tier" {
  for_each         = { for k, s in var.services : k => s if s.tier == "data" }
  target_group_arn = aws_lb_target_group.svc[each.key].arn
  target_id        = var.data_host_instance_id
  port             = each.value.port
}

resource "aws_lb_listener" "http" {
  load_balancer_arn = aws_lb.this.arn
  port              = 80
  protocol          = "HTTP"
  default_action {
    type = "redirect"
    redirect {
      port        = "443"
      protocol    = "HTTPS"
      status_code = "HTTP_301"
    }
  }
}

resource "aws_lb_listener" "https" {
  load_balancer_arn = aws_lb.this.arn
  port              = 443
  protocol          = "HTTPS"
  ssl_policy        = "ELBSecurityPolicy-TLS13-1-2-2021-06"
  certificate_arn   = var.certificate_arn
  default_action {
    type = "fixed-response"
    fixed_response {
      content_type = "text/plain"
      message_body = "not found"
      status_code  = "404"
    }
  }
}

resource "aws_lb_listener_rule" "svc" {
  for_each     = var.services
  listener_arn = aws_lb_listener.https.arn
  priority     = local.priorities[each.key]
  action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.svc[each.key].arn
  }
  condition {
    path_pattern { values = each.value.paths }
  }
}

# ---------------------------------------------------------------- ASG per tier (queue, core)
resource "aws_launch_template" "tier" {
  for_each      = var.asgs
  name_prefix   = "${var.name}-${each.key}-"
  image_id      = data.aws_ssm_parameter.ami.value
  instance_type = each.value.instance_type
  iam_instance_profile { name = var.instance_profile_name }
  vpc_security_group_ids = [var.app_sg_id]
  metadata_options {
    http_tokens                 = "required"
    http_put_response_hop_limit = 2
  }
  block_device_mappings {
    device_name = "/dev/xvda"
    ebs {
      volume_size = 20
      volume_type = "gp3"
      encrypted   = true
    }
  }
  user_data = base64encode(templatefile("${path.module}/user_data.sh.tftpl", {
    name         = var.name, region = var.region, registry = var.registry, image_tag = var.image_tag, log_group = var.log_group,
    data_host_ip = var.data_host_ip,
    services     = sort([for k, s in var.services : k if s.tier == each.key]),
    ports        = local.ports, has_worker = local.has_worker,
  }))
  tag_specifications {
    resource_type = "instance"
    tags          = { Name = "${var.name}-${each.key}", Tier = each.key }
  }
  lifecycle { create_before_destroy = true }
}

resource "aws_autoscaling_group" "tier" {
  for_each            = var.asgs
  name_prefix         = "${var.name}-${each.key}-"
  min_size            = each.value.min
  max_size            = each.value.max
  desired_capacity    = each.value.desired
  vpc_zone_identifier = var.subnet_ids
  target_group_arns   = [for k, s in var.services : aws_lb_target_group.svc[k].arn if s.tier == each.key]
  health_check_type   = "EC2" # HLD: dependency yang mati tidak boleh memicu penggantian instance beruntun
  launch_template {
    id      = aws_launch_template.tier[each.key].id
    version = aws_launch_template.tier[each.key].latest_version
  }
  instance_refresh {
    strategy = "Rolling"
    preferences { min_healthy_percentage = 50 }
  }
  lifecycle { create_before_destroy = true }
}

resource "aws_autoscaling_policy" "cpu" {
  for_each               = var.asgs
  name                   = "${var.name}-${each.key}-cpu60"
  autoscaling_group_name = aws_autoscaling_group.tier[each.key].name
  policy_type            = "TargetTrackingScaling"
  target_tracking_configuration {
    predefined_metric_specification { predefined_metric_type = "ASGAverageCPUUtilization" }
    target_value = 60
  }
}

output "alb_dns_name" { value = aws_lb.this.dns_name }
output "alb_arn_suffix" { value = aws_lb.this.arn_suffix }
output "target_group_arn_suffixes" { value = { for k, tg in aws_lb_target_group.svc : k => tg.arn_suffix } }
