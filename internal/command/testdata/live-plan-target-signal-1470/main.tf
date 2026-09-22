# GitHub issue #1470's end-to-end shape: the block a -target run is about,
# beside an ACM/Route53 validation pair the run excludes, arranged so that
# statelessResolve's second pass CANNOT settle the record's for_each.
#
# projection.PlanInstances plans only blocks with no count and no for_each,
# and its planned values are what let the second pass enumerate a for_each
# that reads a sibling's computed attribute. The certificate here is itself a
# for_each block, so PlanInstances never plans it, the second pass is handed
# nothing about domain_validation_options, and whatever the first pass raised
# about the record is what the run sees.
#
# -target=aws_s3_bucket.data drops both the certificate and the record from
# the plan graph: nothing the bucket reads reaches them.
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

resource "aws_acm_certificate" "cert" {
  for_each          = toset(["example.com"])
  domain_name       = each.value
  validation_method = "DNS"
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.cert["example.com"].domain_validation_options : dvo.domain_name => {
      name = dvo.resource_record_name
      type = dvo.resource_record_type
    }
  }

  zone_id = "Z0423220"
  name    = each.value.name
  type    = each.value.type
  records = ["ignored"]
  ttl     = 60
}
