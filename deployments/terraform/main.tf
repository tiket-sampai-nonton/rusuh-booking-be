data "aws_caller_identity" "this" {}

locals {
  name     = "rusuh-${var.env}"
  registry = "${data.aws_caller_identity.this.account_id}.dkr.ecr.${var.region}.amazonaws.com"
  app_ports = sort(distinct([
    for _, s in var.services : tostring(s.port) if s.tier != "data"
  ]))
  data_alb_ports = [for _, s in var.services : s.port if s.tier == "data"]
}

module "network" {
  source                = "./modules/network"
  name                  = local.name
  vpc_cidr              = var.vpc_cidr
  allowed_ingress_cidrs = var.allowed_ingress_cidrs
  app_ports             = [for p in local.app_ports : tonumber(p)]
  data_alb_ports        = local.data_alb_ports
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
  github_repo                 = var.github_repo
  create_github_oidc_provider = var.create_github_oidc_provider
}

resource "aws_cloudwatch_log_group" "app" {
  name              = "/${local.name}/app"
  retention_in_days = 30 # log tidak boleh memuat data pribadi; retensi tetap dibatasi
}

module "data_host" {
  source                = "./modules/data_host"
  name                  = local.name
  region                = var.region
  subnet_id             = module.network.public_subnet_ids[0]
  security_group_id     = module.network.data_sg_id
  instance_profile_name = module.iam.instance_profile_name
  instance_type         = var.data_host.instance_type
  data_volume_gb        = var.data_host.data_volume_gb
  registry              = local.registry
  image_tag             = var.image_tag
  log_group             = aws_cloudwatch_log_group.app.name
}

module "compute" {
  source                = "./modules/compute"
  name                  = local.name
  region                = var.region
  vpc_id                = module.network.vpc_id
  subnet_ids            = module.network.public_subnet_ids
  alb_sg_id             = module.network.alb_sg_id
  app_sg_id             = module.network.app_sg_id
  instance_profile_name = module.iam.instance_profile_name
  registry              = local.registry
  image_tag             = var.image_tag
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
