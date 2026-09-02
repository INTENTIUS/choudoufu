# The corpus-alb-complete carrier shape, reduced: terraform-aws-modules/acm's
# own aws_route53_record.validation, which reaches domain_validation_options
# through a LOCAL built with distinct()/for/merge() and indexes it with
# count.index rather than each.value - a shape TestManagedResult... above
# (each.value, direct reference) does not exercise at all.
resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

locals {
  validation_domains = distinct([
    for k, v in aws_acm_certificate.cert.domain_validation_options : merge(
      tomap(v), { domain_name = replace(v.domain_name, "*.", "") }
    )
  ])
}

resource "aws_route53_record" "validation" {
  count = 1

  zone_id = "Z0423220"
  name    = element(local.validation_domains, count.index)["resource_record_name"]
  type    = element(local.validation_domains, count.index)["resource_record_type"]
  records = ["ignored"]
  ttl     = 60
}
