# The collision itself, under the loader where it exists.
#
# The for_each IS derived from a managed read, so the block carries the
# provenance that switches #187's classification on. The identity argument
# beside it reads a required root variable nothing sets, and check/load.go
# substitutes cty.UnknownVal for that - so `name` arrives at exactly the same
# branch, holding exactly the same kind of unknown, as an argument waiting on
# a sibling's apply.
#
# Only where the unknown CAME FROM tells the two apart. #183 rules this one
# must stay refused.
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
