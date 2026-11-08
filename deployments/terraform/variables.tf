variable "env" {
  description = "Nama environment (staging | prod)."
  type        = string
  default     = "staging"
  validation {
    condition     = contains(["staging", "prod"], var.env)
    error_message = "env harus staging atau prod."
  }
}

variable "region" {
  type    = string
  default = "ap-southeast-2"
}

variable "owner" {
  description = "Tag owner."
  type        = string
}

variable "expires_on" {
  description = "Tag expires-on (YYYY-MM-DD); PoC wajib di-teardown."
  type        = string
}

variable "vpc_cidr" {
  type    = string
  default = "10.20.0.0/16"
}

variable "behind_cloudflare" {
  description = "true = API lewat proxy Cloudflare: ALB hanya menerima IP Cloudflare dan X-Forwarded-For dari Cloudflare dipertahankan."
  type        = bool
  default     = false
}

variable "allowed_ingress_cidrs" {
  description = "CIDR yang boleh mengakses ALB bila behind_cloudflare=false. Webhook Midtrans juga butuh akses ini."
  type        = list(string)
  default     = []
}

variable "certificate_arn" {
  description = "ACM ARN untuk listener HTTPS (wajib; port 80 hanya mengalihkan ke 443). Token visitor dan webhook Midtrans tidak boleh lewat HTTP."
  type        = string
  validation {
    condition     = startswith(var.certificate_arn, "arn:aws:acm:")
    error_message = "certificate_arn harus ARN ACM."
  }
}

variable "services" {
  description = "Service backend: port dan path ALB. Rule queue selalu dievaluasi pertama (lihat compute)."
  type = map(object({
    port  = number
    paths = list(string)
    tier  = string # queue | core | data
  }))
  default = {
    queue        = { port = 8081, paths = ["/api/v1/queue*", "/api/v1/queue-status/*", "/api/v1/events/*/queue-entries", "/api/v1/queue-entries/*"], tier = "queue" }
    booking      = { port = 8082, paths = ["/api/v1/events", "/api/v1/events/*", "/api/v1/bookings*", "/api/v1/booking-sessions*"], tier = "core" }
    payment      = { port = 8083, paths = ["/api/v1/payments*", "/api/v1/payment-page-sessions", "/api/v1/webhooks/midtrans"], tier = "core" }
    notification = { port = 8084, paths = ["/api/v1/push-subscriptions*"], tier = "data" }
    user         = { port = 8085, paths = ["/api/v1/auth/*", "/api/v1/me*"], tier = "core" }
  }
}

variable "queue_asg" {
  type    = object({ min = number, max = number, desired = number, instance_type = string })
  default = { min = 1, max = 3, desired = 1, instance_type = "t4g.small" }
}

variable "core_asg" {
  type    = object({ min = number, max = number, desired = number, instance_type = string })
  default = { min = 1, max = 2, desired = 1, instance_type = "t4g.small" }
}

variable "data_host" {
  type    = object({ instance_type = string, data_volume_gb = number })
  default = { instance_type = "t4g.small", data_volume_gb = 50 } # t4g.small: tipe tertinggi t4g yang eligible Free plan
}

variable "enable_daily_snapshot" {
  description = "Snapshot harian lewat DLM (simpan 7). SCP Free plan mengizinkan dlm:*; bila apply menolaknya, set false (aws-power.sh off tetap membuat snapshot)."
  type        = bool
  default     = true
}

variable "image_tag" {
  description = "Tag image (immutable) untuk boot pertama, mis. git-<sha>. Setelah itu rilis lewat deploy-aws.sh, bukan terraform apply."
  type        = string
}

variable "github_repo" {
  description = "owner/repo GitHub yang boleh push ke ECR lewat OIDC. Kosong = tanpa role CI. Harus kosong di akun AWS Free plan: SCP-nya menolak iam:*Provider*, jadi provider OIDC tidak bisa dibuat."
  type        = string
  default     = ""
}

variable "create_ci_user" {
  description = "Buat IAM user khusus CI (kredensial statis) untuk akun yang tidak bisa memakai OIDC, mis. AWS Free plan."
  type        = bool
  default     = false
}

variable "create_github_oidc_provider" {
  description = "Buat OIDC provider GitHub (hanya satu per akun)."
  type        = bool
  default     = false
}

variable "budget_limits_usd" {
  description = "Ambang AWS Budgets bulanan (HLD: 50/100/150)."
  type        = list(number)
  default     = [50, 100, 150]
}

variable "alert_email" {
  description = "Penerima alert budget dan alarm."
  type        = string
}
