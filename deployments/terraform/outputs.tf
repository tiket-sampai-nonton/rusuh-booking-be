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

output "github_ci_user" {
  description = "IAM user untuk CI (kosong bila create_ci_user=false). Buat access key-nya manual, lihat panduan 8.10."
  value       = module.iam.github_ci_user
}

output "data_host_instance_id" {
  description = "Target SSM Session Manager untuk administrasi (tanpa SSH)."
  value       = module.data_host.instance_id
}

output "images_bucket" {
  value       = module.storage.images_bucket
  description = "Bucket gambar event; dipakai seed-ssm.sh untuk IMAGE_S3_BUCKET dan IMAGE_PUBLIC_BASE_URL."
}
