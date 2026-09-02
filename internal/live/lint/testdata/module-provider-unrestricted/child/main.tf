variable "region" {
  type = string
}

provider "aws" {
  region = var.region
}

resource "aws_s3_bucket" "data" {
  bucket = "module-provider-unrestricted-child"
}
