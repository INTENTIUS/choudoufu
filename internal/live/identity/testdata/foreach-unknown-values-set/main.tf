# A SET whose elements are unknown. A set's elements are its own keys, so an
# unknown element is an unknown instance address and stock OpenTofu refuses it
# too (internal/lang/evalchecks's performSetValueChecks collapses a set that is
# not wholly known to an unknown value). The map case beside it in
# foreach_keyset.go's forEachKeysKnown must not widen to this one.
resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_route53_record" "cert_validation" {
  for_each = toset([aws_acm_certificate.cert.arn])

  zone_id = "Z0423220"
  name    = each.value
  type    = "CNAME"
  records = ["ignored"]
  ttl     = 60
}
