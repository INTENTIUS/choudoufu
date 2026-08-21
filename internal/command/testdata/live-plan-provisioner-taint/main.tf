# GitHub issue #353's live-plan wiring fixture.
#
# One ordinary marker-tracked cloud resource with a create-time provisioner,
# and a record_store - which is what admits the provisioner at all, and what
# gives the tainted bit a home. The test seeds a taint record into that store
# by hand (a run that could apply would write it, and live-plan never
# applies) and requires live-plan to report the resource as needing
# replacement.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }

  live {
    estate = "provisioner-taint-unit"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_s3_bucket" "app" {
  bucket = "tofu-provisioner-taint-bucket"

  provisioner "local-exec" {
    command = "true"
  }
}
