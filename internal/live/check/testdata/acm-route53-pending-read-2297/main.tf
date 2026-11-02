# GitHub issue #2297: terraform-aws-modules/acm's validation record, reduced.
# zone_id reads a data source, which this offline check never reads, and
# name and type read the certificate's domain_validation_options, which a
# provider plan leaves unknown until the certificate is applied. live-plan
# reads the zone first and classifies the record as waiting on the
# certificate; see TestPendingDataReadLetsASiblingApplyClassify.
variable "fallback_validation_options" {
  type    = any
  default = []
}

locals {
  validation_domains = distinct(
    [for k, v in try(aws_acm_certificate.this[0].domain_validation_options, var.fallback_validation_options) : merge(
      tomap(v), { domain_name = replace(v.domain_name, "*.", "") }
    )]
  )
}

data "aws_route53_zone" "this" {
  name = "example.com"
}

resource "aws_acm_certificate" "this" {
  count = 1

  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_route53_record" "validation" {
  count = 1

  zone_id = data.aws_route53_zone.this.zone_id
  name    = element(local.validation_domains, count.index)["resource_record_name"]
  type    = element(local.validation_domains, count.index)["resource_record_type"]
  ttl     = 60
  records = [element(local.validation_domains, count.index)["resource_record_value"]]
}
