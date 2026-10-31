provider "aws" {
  region = var.region
  default_tags {
    tags = {
      project    = "rusuh"
      env        = var.env
      owner      = var.owner
      expires-on = var.expires_on
      managed-by = "terraform"
    }
  }
}
