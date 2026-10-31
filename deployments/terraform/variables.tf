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
  default = "ap-southeast-3"
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

variable "allowed_ingress_cidrs" {
  description = "CIDR yang boleh mengakses ALB. Tanpa CloudFront/WAF, batasi ke CIDR demo; 0.0.0.0/0 hanya sadar risiko."
  type        = list(string)
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
  description = "Service backend: port dan path ALB (urutan prioritas rule = urutan key)."
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
  default = { instance_type = "t4g.medium", data_volume_gb = 50 }
}

variable "image_tag" {
  description = "Tag image (immutable) yang dijalankan. Gunakan digest/sha commit, bukan latest."
  type        = string
}

variable "github_repo" {
  description = "owner/repo GitHub yang boleh push ke ECR lewat OIDC. Kosong = tanpa role CI."
  type        = string
  default     = ""
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
