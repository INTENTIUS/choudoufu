# Limits fixture: count.index reaching an identity-bearing argument by
# indexing into a collection.
#
# This anchor kept its original name (count-index-in-tag) after #187
# narrowed the rule, because live/LIMITATIONS.md's own docs_ref still points
# here by that slug and nothing forces a rename. What it now demonstrates is
# broader than a tag: aws_network_acl_rule is admitted, count is not itself
# banned, and neither is rule_number as an argument - the problem is that
# rule_number is one of this type's Components (network_acl_id, rule_number,
# protocol, egress - table_generated.go), and here its value is picked out
# of var.rule_numbers at the lexical position count.index rather than built
# as a pure function of count.index itself. #192 narrowed the rule a second
# time: count.index rendered as a pure scalar (100 + count.index, a
# template, a bare argument) no longer trips it, because a plain scalar
# never renumbers on scale-down - OpenTofu always retires the highest count
# index first - so a value built purely from the index names the same
# instance on every run. Indexing into a collection has no such guarantee:
# what sits at position count.index is controlled by the collection, not by
# the index, so reorder or remove an instance and a later instance's marker
# would point at the wrong live NACL rule. RuleCountIndex
# (internal/live/lint/count_index.go, checkCountIndex,
# unsafeCountIndexRanges) still catches this, gated by countIndexScopeForType
# on whether the argument identity.LookupType names for this type could ever
# carry it - the identity-relevance test #187 added - and by
# unsafeCountIndexRanges on whether count.index reaches an IndexExpr's Key
# position - the indexing test #192 added. Check() reports exactly
# RuleCountIndex for this directory, asserted by TestLimitsEnforced
# (internal/live/lint/limits_test.go). See live/LIMITATIONS.md.

variable "rule_numbers" {
  type    = list(number)
  default = [100, 200, 300]
}

resource "aws_network_acl_rule" "this" {
  count = 2

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100 + var.rule_numbers[count.index]
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}
