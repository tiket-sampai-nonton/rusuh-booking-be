variable "name" { type = string }
variable "region" { type = string }
variable "subnet_id" { type = string }
variable "security_group_id" { type = string }
variable "instance_profile_name" { type = string }
variable "instance_type" { type = string }
variable "data_volume_gb" { type = number }
variable "registry" { type = string }
variable "image_tag" { type = string }
variable "log_group" { type = string }

data "aws_ssm_parameter" "ami" {
  name = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-arm64"
}

data "aws_subnet" "this" { id = var.subnet_id }

resource "aws_instance" "this" {
  ami                    = data.aws_ssm_parameter.ami.value
  instance_type          = var.instance_type
  subnet_id              = var.subnet_id
  vpc_security_group_ids = [var.security_group_id]
  iam_instance_profile   = var.instance_profile_name

  metadata_options {
    http_tokens                 = "required" # IMDSv2
    http_put_response_hop_limit = 2          # container perlu mencapai IMDS untuk kredensial instance
  }
  root_block_device {
    volume_type = "gp3"
    volume_size = 20
    encrypted   = true
  }
  user_data = templatefile("${path.module}/user_data.sh.tftpl", {
    name = var.name, region = var.region, registry = var.registry, image_tag = var.image_tag, log_group = var.log_group,
  })
  user_data_replace_on_change = false # mengganti user_data tidak boleh me-recreate host yang memegang data
  tags                        = { Name = "${var.name}-data", Backup = "daily" }
  lifecycle { ignore_changes = [ami, user_data] }
}

# Data PostgreSQL/Valkey/RabbitMQ ada di volume terpisah agar host bisa diganti tanpa kehilangan data.
resource "aws_ebs_volume" "data" {
  availability_zone = data.aws_subnet.this.availability_zone
  size              = var.data_volume_gb
  type              = "gp3"
  encrypted         = true
  tags              = { Name = "${var.name}-data", Backup = "daily" }
  lifecycle { prevent_destroy = true }
}

resource "aws_volume_attachment" "data" {
  device_name = "/dev/sdf"
  volume_id   = aws_ebs_volume.data.id
  instance_id = aws_instance.this.id
}

# Backup: snapshot harian, simpan 7.
data "aws_iam_policy_document" "dlm_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["dlm.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "dlm" {
  name_prefix        = "${var.name}-dlm-"
  assume_role_policy = data.aws_iam_policy_document.dlm_assume.json
}

resource "aws_iam_role_policy_attachment" "dlm" {
  role       = aws_iam_role.dlm.name
  policy_arn = "arn:aws:iam::aws:policy/service-role/AWSDataLifecycleManagerServiceRole"
}

resource "aws_dlm_lifecycle_policy" "daily" {
  description        = "${var.name} snapshot harian"
  execution_role_arn = aws_iam_role.dlm.arn
  state              = "ENABLED"
  policy_details {
    resource_types = ["VOLUME"]
    target_tags    = { Backup = "daily" }
    schedule {
      name = "daily-7"
      create_rule {
        interval      = 24
        interval_unit = "HOURS"
        times         = ["18:00"]
      }
      retain_rule { count = 7 }
      copy_tags = true
    }
  }
}

output "instance_id" { value = aws_instance.this.id }
output "private_ip" { value = aws_instance.this.private_ip }
