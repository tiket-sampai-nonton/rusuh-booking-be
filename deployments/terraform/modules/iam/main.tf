variable "name" { type = string }
variable "env" { type = string }
variable "region" { type = string }
variable "ecr_repository_arns" { type = list(string) }
variable "backup_bucket_arn" { type = string }
variable "images_bucket_arn" { type = string }
variable "github_repo" { type = string }
variable "create_github_oidc_provider" { type = bool }
variable "create_ci_user" { type = bool }

data "aws_caller_identity" "this" {}

data "aws_iam_policy_document" "ec2_assume" {
  statement {
    actions = ["sts:AssumeRole"]
    principals {
      type        = "Service"
      identifiers = ["ec2.amazonaws.com"]
    }
  }
}

resource "aws_iam_role" "instance" {
  name_prefix        = "${var.name}-instance-"
  assume_role_policy = data.aws_iam_policy_document.ec2_assume.json
}

# Akses admin lewat SSM Session Manager; tidak ada SSH.
resource "aws_iam_role_policy_attachment" "ssm" {
  role       = aws_iam_role.instance.name
  policy_arn = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"
}

data "aws_iam_policy_document" "instance" {
  statement {
    sid       = "EcrAuth"
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
  statement {
    sid       = "EcrPull"
    actions   = ["ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer", "ecr:BatchCheckLayerAvailability"]
    resources = var.ecr_repository_arns
  }
  statement {
    sid     = "ReadOwnParameters"
    actions = ["ssm:GetParameter", "ssm:GetParameters", "ssm:GetParametersByPath"]
    resources = [
      "arn:aws:ssm:${var.region}:${data.aws_caller_identity.this.account_id}:parameter/${var.name}/*",
    ]
  }
  statement {
    sid       = "Backups"
    actions   = ["s3:PutObject", "s3:GetObject", "s3:ListBucket"]
    resources = [var.backup_bucket_arn, "${var.backup_bucket_arn}/*"]
  }
  statement {
    sid       = "EventImages"
    actions   = ["s3:PutObject"]
    resources = ["${var.images_bucket_arn}/events/*"]
  }
  statement {
    sid       = "Logs"
    actions   = ["logs:CreateLogStream", "logs:PutLogEvents", "logs:DescribeLogStreams"] # DescribeLogStreams: CloudWatch Agent (EMF NFR-09)
    resources = ["arn:aws:logs:${var.region}:${data.aws_caller_identity.this.account_id}:log-group:/${var.name}/*:*"]
  }
}

resource "aws_iam_role_policy" "instance" {
  name   = "app"
  role   = aws_iam_role.instance.id
  policy = data.aws_iam_policy_document.instance.json
}

resource "aws_iam_instance_profile" "instance" {
  name_prefix = "${var.name}-"
  role        = aws_iam_role.instance.name
}

# --- GitHub Actions OIDC: hanya boleh push image ke ECR repo proyek dari branch main ---
resource "aws_iam_openid_connect_provider" "github" {
  count          = var.create_github_oidc_provider ? 1 : 0
  url            = "https://token.actions.githubusercontent.com"
  client_id_list = ["sts.amazonaws.com"]
}

locals {
  github_provider_arn = var.create_github_oidc_provider ? aws_iam_openid_connect_provider.github[0].arn : "arn:aws:iam::${data.aws_caller_identity.this.account_id}:oidc-provider/token.actions.githubusercontent.com"
}

data "aws_iam_policy_document" "github_assume" {
  count = var.github_repo == "" ? 0 : 1
  statement {
    actions = ["sts:AssumeRoleWithWebIdentity"]
    principals {
      type        = "Federated"
      identifiers = [local.github_provider_arn]
    }
    condition {
      test     = "StringEquals"
      variable = "token.actions.githubusercontent.com:aud"
      values   = ["sts.amazonaws.com"]
    }
    condition {
      test     = "StringLike"
      variable = "token.actions.githubusercontent.com:sub"
      # Job yang memakai `environment:` membawa sub environment, bukan ref.
      values = ["repo:${var.github_repo}:ref:refs/heads/main", "repo:${var.github_repo}:environment:${var.env}"]
    }
  }
}

resource "aws_iam_role" "github_ci" {
  count              = var.github_repo == "" ? 0 : 1
  name_prefix        = "${var.name}-github-ci-"
  assume_role_policy = data.aws_iam_policy_document.github_assume[0].json
}

data "aws_iam_policy_document" "github_ci" {
  statement {
    actions   = ["ecr:GetAuthorizationToken"]
    resources = ["*"]
  }
  statement {
    actions = [
      "ecr:BatchCheckLayerAvailability", "ecr:InitiateLayerUpload", "ecr:UploadLayerPart",
      "ecr:CompleteLayerUpload", "ecr:PutImage", "ecr:BatchGetImage", "ecr:DescribeImages",
    ]
    resources = var.ecr_repository_arns
  }
  # Rilis (deploy-aws.sh): ganti tag, jalankan dokumen deploy notification, lalu instance refresh ASG proyek ini.
  statement {
    sid       = "SetImageTag"
    actions   = ["ssm:PutParameter"]
    resources = ["arn:aws:ssm:${var.region}:${data.aws_caller_identity.this.account_id}:parameter/${var.name}/image_tag"]
  }
  statement {
    sid       = "RunDeployDocument"
    actions   = ["ssm:SendCommand"]
    resources = ["arn:aws:ssm:${var.region}:${data.aws_caller_identity.this.account_id}:document/${var.name}-deploy-notification"]
  }
  statement {
    sid       = "OnDataHostOnly"
    actions   = ["ssm:SendCommand"]
    resources = ["arn:aws:ec2:${var.region}:${data.aws_caller_identity.this.account_id}:instance/*"]
    condition {
      test     = "StringEquals"
      variable = "ssm:resourceTag/Name"
      values   = ["${var.name}-data"]
    }
  }
  statement {
    sid       = "RefreshOwnAsg"
    actions   = ["autoscaling:StartInstanceRefresh"]
    resources = ["arn:aws:autoscaling:${var.region}:${data.aws_caller_identity.this.account_id}:autoScalingGroup:*:autoScalingGroupName/${var.name}-*"]
  }
  # Aksi Describe/Get ini tidak mendukung pembatasan per resource.
  statement {
    sid = "ReadDeployStatus"
    actions = [
      "ssm:GetCommandInvocation", "ec2:DescribeInstances",
      "autoscaling:DescribeAutoScalingGroups", "autoscaling:DescribeInstanceRefreshes",
    ]
    resources = ["*"]
  }
}

resource "aws_iam_role_policy" "github_ci" {
  count  = var.github_repo == "" ? 0 : 1
  name   = "ecr-push-deploy"
  role   = aws_iam_role.github_ci[0].id
  policy = data.aws_iam_policy_document.github_ci.json
}

# Akun AWS Free plan: SCP menolak iam:*Provider* dan iam:CreateGroup, jadi OIDC dan grup tidak ada. Pengganti: satu IAM user
# khusus CI dengan policy yang sama dengan role OIDC di atas. Access key dibuat manual (tidak masuk state Terraform).
#trivy:ignore:AVD-AWS-0143
resource "aws_iam_user" "github_ci" {
  count = var.create_ci_user ? 1 : 0
  name  = "${var.name}-github-ci"
}

#trivy:ignore:AVD-AWS-0143
resource "aws_iam_user_policy" "github_ci" {
  count  = var.create_ci_user ? 1 : 0
  name   = "ecr-push-deploy"
  user   = aws_iam_user.github_ci[0].name
  policy = data.aws_iam_policy_document.github_ci.json
}

output "github_ci_user" { value = try(aws_iam_user.github_ci[0].name, "") }
output "instance_profile_name" { value = aws_iam_instance_profile.instance.name }
output "github_ci_role_arn" { value = try(aws_iam_role.github_ci[0].arn, "") }
