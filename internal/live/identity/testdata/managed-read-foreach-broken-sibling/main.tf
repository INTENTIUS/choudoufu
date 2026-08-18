# The #187 carrier shape with one component broken for a reason no apply
# settles: zone_id calls a function that returns a different value on every
# call. name and type still wait on the certificate's apply, so this is the
# case where a sibling-apply refusal and an unrelated one stand together and
# the instance must stay REFUSED rather than be classified.
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

  zone_id = uuid()
  name    = each.value.name
  type    = each.value.type
  records = ["ignored"]
  ttl     = 60
}
