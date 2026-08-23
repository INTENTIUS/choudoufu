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

# GitHub issue #369: terraform-aws-modules/terraform-aws-security-group's own
# computed_ingress_with_source_security_group_id resource (main.tf, v5.3.1)
# sets BOTH source_security_group_id (a real value) and prefix_list_ids
# (var.ingress_prefix_list_ids, defaulted here to []) on the very same
# instance. cidr_blocks/ipv6_cidr_blocks/prefix_list_ids/
# source_security_group_id is one alternation, and prefix_list_ids being a
# zero-element list is not "unclear which of the four supplies the
# identity" - the sibling source_security_group_id already answers that -
# so this must resolve concretely off the sibling, not refuse as
# "Ambiguous list-valued identity argument".
variable "empty_prefix_list_ids" {
  type    = list(string)
  default = []
}

resource "aws_security_group_rule" "resolved_by_sibling" {
  security_group_id         = "sg-0123456789abcdef0"
  type                      = "ingress"
  protocol                  = "tcp"
  from_port                 = 80
  to_port                   = 80
  source_security_group_id  = "sg-fedcba9876543210f"
  prefix_list_ids           = var.empty_prefix_list_ids
}

# The genuine-ambiguity case #369 says must still refuse: every alternation
# member is a definite empty list (or absent), so NONE of them settles the
# identity - a set of members that each contribute nothing is exactly the
# existing all-absent case, and must read as "Identity argument not set",
# never resolve to a guessed value.
resource "aws_security_group_rule" "all_empty_no_sibling" {
  security_group_id = "sg-0123456789abcdef0"
  type               = "ingress"
  protocol           = "tcp"
  from_port          = 8080
  to_port            = 8080
  cidr_blocks        = var.empty_prefix_list_ids
  prefix_list_ids    = var.empty_prefix_list_ids
}

# GitHub issue #384: terraform-aws-modules/security-group's own
# egress_rules = ["all-all"] expands into an instance exactly like this one
# - both egress_cidr_blocks and egress_ipv6_cidr_blocks default to a real,
# non-empty one-element list, so BOTH cidr_blocks and ipv6_cidr_blocks carry
# a genuine value at once. This is not the zero-element case above: neither
# candidate is empty, so nothing "defers to a sibling" - the two candidates
# disagree about which live object (AWS creates one rule per IP family) this
# one declared instance names, and picking either is a guess. This must
# never resolve to a concrete import ID; it must refuse "Ambiguous
# list-valued identity argument" naming both cidr_blocks and
# ipv6_cidr_blocks (or, where a live block's record_store makes the type's
# identity fully recordable, drop to ClassRecordLocated - see
# solelementconflict_test.go).
resource "aws_security_group_rule" "egress_all_all" {
  security_group_id = "sg-0123456789abcdef0"
  type               = "egress"
  protocol           = "-1"
  from_port          = 0
  to_port            = 0
  cidr_blocks        = ["0.0.0.0/0"]
  ipv6_cidr_blocks   = ["::/0"]
}
