# The collision moved onto the KEY SET, which is the one place the other
# fixtures in this pair cannot reach.
#
# managed-result-foreach-unset-var and -mixed-var both put the unset variable
# in an identity ARGUMENT, so the instance addresses are settled either way
# and only the marker's value is in doubt. Here the variable is inside the
# for_each comprehension's key expression: with the certificate's planned
# values in hand, dvo.domain_name is known and var.suffix is not, so the key
# is unknown.
#
# Folding that key set anyway would invent an instance ADDRESS out of an unset
# variable - a fabricated address is a fabricated marker, and every run with a
# different tfvars file would own a different object. #183 rules this must
# stay refused.
variable "suffix" {
  type = string
}

resource "aws_acm_certificate" "cert" {
  domain_name       = "example.com"
  validation_method = "DNS"
}

resource "aws_route53_record" "cert_validation" {
  for_each = {
    for dvo in aws_acm_certificate.cert.domain_validation_options : "${dvo.domain_name}${var.suffix}" => {
      name = dvo.resource_record_name
      type = dvo.resource_record_type
    }
  }

  zone_id = "Z0423220"
  name    = each.value.name
  type    = "CNAME"
  records = ["ignored"]
  ttl     = 60
}
