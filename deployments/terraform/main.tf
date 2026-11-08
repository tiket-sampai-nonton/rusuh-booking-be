data "aws_caller_identity" "this" {}

locals {
  name     = "rusuh-${var.env}"
  registry = "${data.aws_caller_identity.this.account_id}.dkr.ecr.${var.region}.amazonaws.com"
  app_ports = sort(distinct([
    for _, s in var.services : tostring(s.port) if s.tier != "data"
  ]))
  data_alb_ports = [for _, s in var.services : s.port if s.tier == "data"]
  # Sumber: https://www.cloudflare.com/ips-v4 (diambil 2026-11-08); perbarui bila Cloudflare mengumumkan rentang baru.
  cloudflare_ipv4 = [
    "173.245.48.0/20", "103.21.244.0/22", "103.22.200.0/22", "103.31.4.0/22", "141.101.64.0/18",
    "108.162.192.0/18", "190.93.240.0/20", "188.114.96.0/20", "197.234.240.0/22", "198.41.128.0/17",
    "162.158.0.0/15", "104.16.0.0/13", "104.24.0.0/14", "172.64.0.0/13", "131.0.72.0/22",
  ]
}

# Dibaca instance saat boot; deploy-aws.sh yang mengubah nilainya, jadi Terraform tidak boleh mengembalikannya.
resource "aws_ssm_parameter" "image_tag" {
  name  = "/${local.name}/image_tag"
  type  = "String"
  value = var.image_tag
  lifecycle { ignore_changes = [value] }
}

module "network" {
  source                = "./modules/network"
  name                  = local.name
  vpc_cidr              = var.vpc_cidr
  allowed_ingress_cidrs = var.behind_cloudflare ? local.cloudflare_ipv4 : var.allowed_ingress_cidrs
  app_ports             = [for p in local.app_ports : tonumber(p)]
  data_alb_ports        = local.data_alb_ports
  booking_port          = var.services["booking"].port
}

module "ecr" {
  source       = "./modules/ecr"
  name         = local.name
  repositories = keys(var.services)
}

module "storage" {
  source = "./modules/storage"
  name   = local.name
}

module "iam" {
  source                      = "./modules/iam"
  name                        = local.name
  env                         = var.env
  region                      = var.region
  ecr_repository_arns         = module.ecr.repository_arns
  backup_bucket_arn           = module.storage.backup_bucket_arn
  images_bucket_arn           = module.storage.images_bucket_arn
  github_repo                 = var.github_repo
  create_github_oidc_provider = var.create_github_oidc_provider
  create_ci_user              = var.create_ci_user
}

resource "aws_cloudwatch_log_group" "app" {
  name              = "/${local.name}/app"
  retention_in_days = 30 # log tidak boleh memuat data pribadi; retensi tetap dibatasi
}

module "data_host" {
  source                = "./modules/data_host"
  depends_on            = [aws_ssm_parameter.image_tag]
  name                  = local.name
  region                = var.region
  subnet_id             = module.network.public_subnet_ids[0]
  security_group_id     = module.network.data_sg_id
  instance_profile_name = module.iam.instance_profile_name
  instance_type         = var.data_host.instance_type
  data_volume_gb        = var.data_host.data_volume_gb
  enable_daily_snapshot = var.enable_daily_snapshot
  registry              = local.registry
  log_group             = aws_cloudwatch_log_group.app.name
}

module "compute" {
  source                = "./modules/compute"
  depends_on            = [aws_ssm_parameter.image_tag]
  name                  = local.name
  region                = var.region
  vpc_id                = module.network.vpc_id
  subnet_ids            = module.network.public_subnet_ids
  alb_sg_id             = module.network.alb_sg_id
  internal_alb_sg_id    = module.network.internal_alb_sg_id
  app_sg_id             = module.network.app_sg_id
  instance_profile_name = module.iam.instance_profile_name
  registry              = local.registry
  preserve_xff          = var.behind_cloudflare
  log_group             = aws_cloudwatch_log_group.app.name
  certificate_arn       = var.certificate_arn
  data_host_instance_id = module.data_host.instance_id
  data_host_ip          = module.data_host.private_ip
  services              = var.services
  asgs                  = { queue = var.queue_asg, core = var.core_asg }
}

module "observability" {
  source                    = "./modules/observability"
  name                      = local.name
  alert_email               = var.alert_email
  budget_limits_usd         = var.budget_limits_usd
  alb_arn_suffix            = module.compute.alb_arn_suffix
  target_group_arn_suffixes = module.compute.target_group_arn_suffixes
}
