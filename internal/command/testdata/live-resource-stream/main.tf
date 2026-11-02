# live-block-record-store's estate with the bucket ordered after the VPC,
# so a -resource=aws_s3_bucket.data cone holds both and plans the VPC first
# (GitHub issue #2260: TestLivePlanResource_streamsRecordsAsThePlanWalks).
terraform {
  live {
    estate = "live-unit"

    record_store "local" {}
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

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

resource "aws_s3_bucket" "data" {
  bucket = "tofu-live-unit-data"

  depends_on = [aws_vpc.main]
}
