# The #183 collision, built on purpose: the for_each genuinely IS derived
# from a managed read, so the block's provenance is set, and yet one identity
# argument reads a root variable instead of each.value. Under
# internal/live/check's loader that variable arrives as the same cty.Unknown
# a managed read produces, so this is the shape that would misclassify if the
# discriminator tested for an unknown rather than for where it came from.
variable "record_name" {
  type = string
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

  zone_id = "Z0423220"
  name    = var.record_name
  type    = "CNAME"
  records = ["ignored"]
  ttl     = 60
}
