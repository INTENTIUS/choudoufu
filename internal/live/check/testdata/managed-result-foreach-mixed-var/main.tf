# The collision at its sharpest: ONE expression with two possible sources of
# unknown, one of them a required root variable nothing sets.
#
# `name` reads each.value.name - which the block's managed-derived for_each
# makes attributable - and var.prefix in the same template. Both arrive as
# cty.Unknown under check/load.go's substitution, and nothing in the value
# says which one made the result unknown.
#
# managed-result-foreach-unset-var is the easy half of this pair: there the
# argument names no each.* at all, so neither leg of the discriminator can
# reach it whatever the rules say. This one CAN be reached, and is refused
# only because an expression naming a variable is never attributed. Remove
# that rule and this fixture is what notices.
variable "prefix" {
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
  name    = "${var.prefix}${each.value.name}"
  type    = "CNAME"
  records = ["ignored"]
  ttl     = 60
}
