# Issue #240, site 5: resolver.soleElementFromValue evaluated the argument,
# checked IsNull and IsWhollyKnown, and then called ElementIterator on it -
# which panics on a marked value. A list-typed sensitive variable set on an
# identity-bearing argument is all it takes.
#
# The shape is identity/testdata/sole-element-from-value's, which is
# cyhy-amis's *_security_group_rules.tf verbatim (cidr_blocks =
# var.trusted_ingress_networks_ipv4). This package's mark-injection sweep
# found the crash there, and it reproduces on .corpus/cyhy-amis/terraform
# itself once that variable is marked.
variable "trusted" {
  type      = list(string)
  default   = ["10.0.0.0/8"]
  sensitive = true
}

resource "aws_security_group_rule" "from_default" {
  security_group_id = "sg-0123456789abcdef0"
  type              = "ingress"
  protocol          = "tcp"
  from_port         = 22
  to_port           = 22
  cidr_blocks       = var.trusted
}
