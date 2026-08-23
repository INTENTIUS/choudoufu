locals {
  create_certificate = var.create_certificate

  validation_domains = distinct([
    for k, v in try(aws_acm_certificate.this[0].domain_validation_options, var.acm_certificate_domain_validation_options) : merge(
      tomap(v), { domain_name = replace(v.domain_name, "*.", "") }
    )
  ])
}

resource "aws_acm_certificate" "this" {
  count = local.create_certificate ? 1 : 0

  domain_name       = var.domain_name
  validation_method = "DNS"
}

resource "aws_route53_record" "validation" {
  count = local.create_certificate ? 1 : 0

  zone_id = var.zone_id
  name    = element(local.validation_domains, count.index)["resource_record_name"]
  type    = element(local.validation_domains, count.index)["resource_record_type"]
  records = [element(local.validation_domains, count.index)["resource_record_value"]]
  ttl     = 60
}
