# Limits fixture: count.index reaching an identity-bearing argument.
#
# This anchor kept its original name (count-index-in-tag) after #187
# narrowed the rule, because live/LIMITATIONS.md's own docs_ref still points
# here by that slug and nothing forces a rename. What it now demonstrates is
# broader than a tag: aws_network_acl_rule is admitted, count is not itself
# banned, and neither is rule_number as an argument - the problem is that
# rule_number is one of this type's Components (network_acl_id, rule_number,
# protocol, egress - table_generated.go), so count.index here is the exact
# reordering hazard "Banned, and why" describes: reorder or remove an
# instance and every later instance's marker would point at the wrong live
# NACL rule. RuleCountIndex (internal/live/lint/count_index.go,
# checkCountIndex) still catches this, gated by countIndexScopeForType on
# whether the argument identity.LookupType names for this type could ever
# carry it - the identity-relevance test #187 added. Check() reports exactly
# RuleCountIndex for this directory, asserted by TestLimitsEnforced
# (internal/live/lint/limits_test.go). See live/LIMITATIONS.md.

resource "aws_network_acl_rule" "this" {
  count = 2

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100 + count.index
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}
