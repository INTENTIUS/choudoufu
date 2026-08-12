terraform {
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
  bucket = "tofu-stateless-unit-data"
}

# Outside the stateless subset: a logical resource exists only inside the
# record that stateless mode removes.
resource "random_pet" "name" {
  length = 2
}
