# Limits fixture: count.index reaching an identity-bearing argument through
# a collection whose entries repeat, so two instances resolve to one live
# object.
#
# This anchor kept its original name (count-index-in-tag) after #187
# narrowed the rule, because live/LIMITATIONS.md's own docs_ref still points
# here by that slug and nothing forces a rename. What it demonstrates is
# broader than a tag: aws_network_acl_rule is admitted, count is not itself
# banned, and neither is rule_number as an argument - the problem is that
# rule_number is one of this type's Components (network_acl_id, rule_number,
# protocol, egress - table_generated.go), and here its value collides.
#
# var.rule_numbers is [100, 200, 100] and count is 3, so instances [0] and
# [2] both render rule_number = 200 and both would claim the same live NACL
# rule. That collision, not the fact that a collection is indexed, is what
# RuleCountIndex refuses. The refusal is decided by rendering the three
# values and finding two the same (internal/live/lint/count_index_domain.go,
# countIndexDomain.verdict), after the syntactic analysis in
# count_index.go declines to prove an index expression's key position
# injective. Give this list three different entries and the same
# configuration is admitted, correctly: nothing collides.
#
# The history is worth keeping because it is why the rule is shaped this
# way. #192 narrowed it to leave a pure scalar unrefused; #217 reopened it
# after 100 + (count.index %% 3) shipped as "safe" at count = 5, where
# indices 0 and 3 both render 100, and inverted the analysis to refuse
# unless provably injective. Rendering the actual values is the same
# question asked where it can be answered exactly, on the domain the count
# actually has.
#
# Check() reports exactly RuleCountIndex for this directory, asserted by
# TestLimitsEnforced (internal/live/lint/limits_test.go). See
# live/LIMITATIONS.md.

variable "rule_numbers" {
  type    = list(number)
  default = [100, 200, 100]
}

resource "aws_network_acl_rule" "this" {
  count = 3

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100 + var.rule_numbers[count.index]
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}
