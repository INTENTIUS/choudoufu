# Component.SoleElement's narrowing beyond a syntactic list construct: a
# list-typed variable evaluated with its declared default. cyhy-amis's
# *_security_group_rules.tf files use exactly this shape (cidr_blocks =
# var.trusted_ingress_networks_ipv4, default = ["0.0.0.0/0"]) with no
# tfvars shipped, so the default is the only value ever measured. "one"
# has exactly one element and must resolve; "many" has two and must still
# refuse - the AWS API, not list order, decides how more than one CIDR
# composes into the rule's identity.

variable "one" {
  type    = list(string)
  default = ["10.0.0.0/8"]
}

variable "many" {
  type    = list(string)
  default = ["10.0.0.0/8", "192.168.0.0/16"]
}

resource "aws_security_group_rule" "from_default" {
  security_group_id = "sg-0123456789abcdef0"
  type               = "ingress"
  protocol           = "tcp"
  from_port          = 22
  to_port             = 22
  cidr_blocks        = var.one
}

resource "aws_security_group_rule" "ambiguous" {
  security_group_id = "sg-0123456789abcdef0"
  type               = "ingress"
  protocol           = "tcp"
  from_port          = 443
  to_port             = 443
  cidr_blocks        = var.many
}
