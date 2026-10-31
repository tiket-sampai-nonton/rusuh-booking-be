output "alb_dns_name" {
  description = "Alamat ALB (titik masuk; tanpa CloudFront/WAF)."
  value       = module.compute.alb_dns_name
}

output "ecr_repository_urls" {
  value = module.ecr.repository_urls
}

output "backup_bucket" {
  value = module.storage.backup_bucket
}

output "github_ci_role_arn" {
  description = "Role OIDC untuk push image dari GitHub Actions (kosong bila github_repo tidak diisi)."
  value       = module.iam.github_ci_role_arn
}

output "data_host_instance_id" {
  description = "Target SSM Session Manager untuk administrasi (tanpa SSH)."
  value       = module.data_host.instance_id
}
