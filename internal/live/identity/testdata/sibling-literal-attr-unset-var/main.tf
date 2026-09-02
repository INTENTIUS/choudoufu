# A sibling's literal argument set from a variable with no value must
# refuse - not resolve to the empty string. #220's fix reads the same
# expression [resolver.resolveExpr] already reads for an ordinary identity
# component, so an unset var.unset_name fails exactly where it always has.

variable "unset_name" {
  type = string
}

resource "test_sibling" "s" {
  key         = "s-1"
  literal_val = var.unset_name
}

resource "aws_route53_record" "a" {
  zone_id = "Z1"
  name    = test_sibling.s.literal_val
  type    = "TXT"
  ttl     = 300
  records = ["x"]
}
