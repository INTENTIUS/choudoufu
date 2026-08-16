# Fixture: count.index reaching an identity-bearing argument in a way that
# makes two instances render the SAME value. Every resource below is
# refused, and the reason each is refused is a real collision, not a
# disliked shape - TestCountIndexAdmittedShapesRenderDistinctIdentities
# resolves them through internal/live/identity and asserts the duplicate
# import IDs exist.
#
# var.rule_numbers repeats deliberately: [100, 200, 100, 200] over count =
# 3 sends indices 0 and 2 to the same value, which is what makes
# collection-indexing at count.index a hazard HERE. The same shape over a
# collection whose entries differ is admitted, and lives in
# testdata/count-index-pure-scalar as "distinct_collection". That pairing
# is the point: the shape is not what decides it.
#
# All resources are aws_network_acl_rule or aws_route53_record, whose
# Components (internal/live/identity/table_generated.go) name
# rule_number/network_acl_id/protocol/egress and zone_id/name/type as
# identity-bearing, so a collision in these arguments is a collision in the
# live identity itself. testdata/count-index-not-relevant holds the mirror
# image for scope: indexing in an argument none of this data marks as
# identity-bearing.

variable "rule_numbers" {
  type    = list(number)
  default = [100, 200, 100, 200]
}

# A direct collection index: rule_number picks its value out of
# var.rule_numbers at position count.index, rather than being built from
# count.index itself.
resource "aws_network_acl_rule" "list_index" {
  count = 3

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = var.rule_numbers[count.index]
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

# The same hazard through an identity-bearing template attribute: name is
# part of aws_route53_record's ZONEID_NAME_TYPE import identity, and here it
# is built from a value looked up in var.rule_numbers at count.index rather
# than from count.index itself.
resource "aws_route53_record" "list_index_in_template" {
  count = 3

  zone_id = "Z0123456789ABCDEFGHI"
  name    = "record-${var.rule_numbers[count.index]}.example.com"
  type    = "A"
  ttl     = 300
  records = ["10.0.0.1"]
}

# An offset index: still indexing into a collection even though the key
# expression is arithmetic rather than a bare traversal - the whole Key
# subtree of an index expression is in scope, not just a bare
# count.index reference inside it.
resource "aws_network_acl_rule" "offset_index" {
  count = 3

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = var.rule_numbers[count.index + 1]
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

# The same indexing hazard spelled with element() instead of `[]` - this
# project's own reviewed corpus uses element() for exactly this (picking a
# value out of a splat expression no `[]` syntax can index at all), so this
# is not a hypothetical shape.
resource "aws_network_acl_rule" "element_accessor" {
  count = 3

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = element(var.rule_numbers, count.index)
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

# A conditional whose own condition depends on count.index: with count > 2
# this selects one of only two possible values regardless of how many
# instances exist, so distinct indices are not guaranteed distinct values -
# a real collision hazard, not merely a reordering one. Contrast
# testdata/count-index-pure-scalar's "conditional_on_other_value" resource,
# whose condition does not depend on count.index and stays unrefused.
resource "aws_network_acl_rule" "conditional" {
  count = 3

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = count.index == 0 ? 100 : 200
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

# A tuple literal is heterogeneous: indexing it at count.index yields a
# string at one index and a number at another. Both render to the marker
# "100", so the two instances claim one live record - but the two cty
# values are not structurally equal, because their TYPES differ, so a
# distinctness check that compared values alone would call this injective
# and admit it. count_index_domain.go requires one type across the whole
# range before it trusts inequality at all, which is what refuses this.
resource "aws_route53_record" "heterogeneous_tuple" {
  count = 2

  zone_id = "Z0123456789ABCDEFGHI"
  name    = ["100", 100][count.index]
  type    = "A"
  ttl     = 300
  records = ["10.0.0.1"]
}
