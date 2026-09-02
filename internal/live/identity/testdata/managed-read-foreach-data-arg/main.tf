# A block whose for_each genuinely IS derived from a managed read - so its
# expansion carries the provenance - with the failing identity argument
# reading a DATA SOURCE instead of each.value.
#
# The zone this run was handed has no zone_id. That has nothing to do with
# the certificate, and reporting it as "waiting on aws_acm_certificate.cert
# to be applied" would send an operator to apply a certificate that would not
# help. It is the fixture for the rule that the each-carried provenance
# applies only to an argument that actually reads each.*.
data "aws_route53_zone" "main" {
  name = "example.com."
}

resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.cert.domain_validation_options : dvo.domain_name => {
      name = dvo.resource_record_name
      type = dvo.resource_record_type
    }
  }

  zone_id = data.aws_route53_zone.main.zone_id
  name    = "static.example.com."
  type    = "CNAME"
  records = ["ignored"]
  ttl     = 60
}
