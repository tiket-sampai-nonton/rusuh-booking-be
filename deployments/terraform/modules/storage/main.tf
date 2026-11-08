variable "name" { type = string }

data "aws_caller_identity" "this" {}

# Backup dump PostgreSQL dan konfigurasi. Versioned, terenkripsi, tanpa akses publik.
resource "aws_s3_bucket" "backup" {
  bucket        = "${var.name}-backup-${data.aws_caller_identity.this.account_id}"
  force_destroy = false
}

resource "aws_s3_bucket_public_access_block" "backup" {
  bucket                  = aws_s3_bucket.backup.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

resource "aws_s3_bucket_versioning" "backup" {
  bucket = aws_s3_bucket.backup.id
  versioning_configuration { status = "Enabled" }
}

# SSE-S3 (AES256) cukup untuk PoC; CMK menambah biaya dan kompleksitas. Naikkan ke SSE-KMS untuk production.
#trivy:ignore:AVD-AWS-0132
resource "aws_s3_bucket_server_side_encryption_configuration" "backup" {
  bucket = aws_s3_bucket.backup.id
  rule {
    apply_server_side_encryption_by_default { sse_algorithm = "AES256" }
  }
}

resource "aws_s3_bucket_lifecycle_configuration" "backup" {
  bucket = aws_s3_bucket.backup.id
  rule {
    id     = "expire"
    status = "Enabled"
    filter {}
    noncurrent_version_expiration { noncurrent_days = 30 }
    expiration { days = 90 }
  }
}

data "aws_iam_policy_document" "tls_only" {
  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [aws_s3_bucket.backup.arn, "${aws_s3_bucket.backup.arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
}

resource "aws_s3_bucket_policy" "backup" {
  bucket = aws_s3_bucket.backup.id
  policy = data.aws_iam_policy_document.tls_only.json
}

output "backup_bucket" { value = aws_s3_bucket.backup.bucket }
output "backup_bucket_arn" { value = aws_s3_bucket.backup.arn }

# Gambar event hasil upload admin (IMAGE_STORAGE=s3). Gambar memang konten publik: hanya events/* yang bisa dibaca,
# listing bucket tetap tertutup, dan IMAGE_PUBLIC_BASE_URL menunjuk ke bucket. shortcut: tanpa CDN, tambah CDN bila trafik gambar besar.
#trivy:ignore:AVD-AWS-0089
resource "aws_s3_bucket" "images" {
  bucket        = "${var.name}-images-${data.aws_caller_identity.this.account_id}"
  force_destroy = false
}

# Sengaja: bucket gambar punya kebijakan baca publik untuk events/* saja.
#trivy:ignore:AVD-AWS-0087
#trivy:ignore:AVD-AWS-0093
resource "aws_s3_bucket_public_access_block" "images" {
  bucket                  = aws_s3_bucket.images.id
  block_public_acls       = true
  block_public_policy     = false # kebijakan publicRead di bawah
  ignore_public_acls      = true
  restrict_public_buckets = false
}

#trivy:ignore:AVD-AWS-0132
resource "aws_s3_bucket_server_side_encryption_configuration" "images" {
  bucket = aws_s3_bucket.images.id
  rule {
    apply_server_side_encryption_by_default { sse_algorithm = "AES256" }
  }
}

# Objek bernama acak dan tidak pernah ditimpa, jadi versioning tidak diperlukan; unggahan yang tak selesai dibersihkan.
resource "aws_s3_bucket_lifecycle_configuration" "images" {
  bucket = aws_s3_bucket.images.id
  rule {
    id     = "abort-incomplete"
    status = "Enabled"
    filter {}
    abort_incomplete_multipart_upload { days_after_initiation = 1 }
  }
}

data "aws_iam_policy_document" "images_tls_only" {
  statement {
    sid       = "DenyInsecureTransport"
    effect    = "Deny"
    actions   = ["s3:*"]
    resources = [aws_s3_bucket.images.arn, "${aws_s3_bucket.images.arn}/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }
  statement {
    sid       = "PublicReadEventImages"
    actions   = ["s3:GetObject"]
    resources = ["${aws_s3_bucket.images.arn}/events/*"]
    principals {
      type        = "*"
      identifiers = ["*"]
    }
  }
}

resource "aws_s3_bucket_policy" "images" {
  bucket     = aws_s3_bucket.images.id
  policy     = data.aws_iam_policy_document.images_tls_only.json
  depends_on = [aws_s3_bucket_public_access_block.images]
}

output "images_bucket" { value = aws_s3_bucket.images.bucket }
output "images_bucket_arn" { value = aws_s3_bucket.images.arn }
