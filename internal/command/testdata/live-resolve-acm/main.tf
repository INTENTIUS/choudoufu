# The ACM/Route53 validation shape, which a first resolution pass refuses for
# a value the provider derives during PlanResourceChange and no schema
# records. It is the same fixture internal/live/check carries as
# managed-result-foreach; this copy exists so the COMMAND-layer second pass
# (statelessResolve) can be measured on it.
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

  zone_id = "Z0423220"
  name    = each.value.name
  type    = each.value.type
  records = ["ignored"]
  ttl     = 60
}
