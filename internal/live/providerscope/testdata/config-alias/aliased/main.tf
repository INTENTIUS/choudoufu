terraform {
  required_providers {
    aws = {
      source                = "hashicorp/aws"
      configuration_aliases = [aws.primary]
    }
  }
}

resource "aws_s3_bucket" "data" {
  provider = aws.primary

  bucket = "aliased-bucket"
}
