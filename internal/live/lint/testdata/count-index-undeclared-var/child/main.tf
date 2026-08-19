# The child of testdata/count-index-undeclared-var. rule_number is one of
# aws_network_acl_rule's identity-bearing Components, so building it from
# count.index is exactly what puts checkCountIndex on this resource and makes
# it compute the count domain - which is what reaches back across the
# module-call boundary into the caller's binary-operator argument.
#
# It is a collection index rather than a bare count.index on purpose: a bare
# count.index is injective on its own and is admitted without the domain
# mattering, which would leave the verdict unable to tell "the domain came
# back unprovable" apart from "the domain was never consulted". Indexing a
# collection is only safe when the entries at the reachable indices differ,
# so this shape needs the real count range - and refuses when, as here,
# nobody can compute one. testdata/count-index carries the same shape with a
# computable count.

variable "rule_count" {
  type = number
}

variable "rule_numbers" {
  type    = list(number)
  default = [100, 200, 100, 200]
}

resource "aws_network_acl_rule" "r" {
  count = var.rule_count

  network_acl_id = "acl-0123456789abcdef0"
  rule_number    = var.rule_numbers[count.index]
  egress         = false
  protocol       = "tcp"
  rule_action    = "allow"
  cidr_block     = "10.0.0.0/16"
  from_port      = 80
  to_port        = 80
}
