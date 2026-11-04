terraform {
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.71.0"
    }
  }
}

provider "aws" {
  region                      = "us-east-2"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  s3_use_path_style           = true
  default_tags {
    tags = { Project = "choudoufu-2309-repro" }
  }
}

variable "sfx" {
  type    = string
  default = "cdf2309"
}
