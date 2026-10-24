# A configuration that asks for both live mode and a state backend. The
# decoder ignores the backend with a warning (issue #2213).
terraform {
  live {
    estate = "live-unit"
  }

  backend "local" {
    path = "somewhere.tfstate"
  }

  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_s3_bucket" "data" {
  bucket = "tofu-live-unit-data"
}
