# GitHub issue #1470's end-to-end shape: the block a -target run is about,
# beside a pair the run excludes, arranged so that statelessResolve's second
# pass CANNOT settle the excluded record's for_each.
#
# projection.PlanInstances plans only blocks with no count and no for_each,
# and its planned values are what let the second pass enumerate a for_each
# that reads a sibling's computed attribute. The source block here is itself
# a for_each block, so PlanInstances never plans it, the second pass is
# handed nothing about validation_options, and whatever the first pass
# raised about the record is what the run sees.
#
# The source is an aws_cloudwatch_log_group rather than the ACM certificate
# the shape comes from, because the certificate's identity is server-assigned
# and an out-of-scope block that needs discovery reaches the estate sweep,
# whose needs-discovery set is still the whole configuration (the gap PR
# #1471 records beside #1257). A log group is named by its configuration,
# so nothing about it is asked of the cloud. The test's own schema gives it
# the computed set attribute the record iterates; the type is a stand-in for
# the mechanism, and the mechanism is the for_each on the source.
#
# -target=aws_s3_bucket.data drops both the source and the record from the
# plan graph: nothing the bucket reads reaches them.
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

resource "aws_cloudwatch_log_group" "certs" {
  for_each = toset(["example.com"])
  name     = "/certs/${each.value}"
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_cloudwatch_log_group.certs["example.com"].validation_options : dvo.domain_name => {
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
