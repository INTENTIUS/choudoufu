# Fixture: count.index used to index into a collection, call one of
# countIndexAccessorFunctions, or select a conditional's branch, landing in
# an identity-bearing argument. These are the shapes unsafeCountIndexRanges
# (count_index.go) still flags: a value picked out of an external
# collection at the lexical position count.index, or one of finitely many
# branches selected by count.index, rather than a value built as a pure,
# injective function of the index itself. All resources below are
# aws_network_acl_rule or aws_route53_record, whose Components
# (internal/live/identity/table_generated.go) name
# rule_number/network_acl_id/protocol/egress and zone_id/name/type as
# identity-bearing - so count.index reaching one of those this way is
# still exactly the collision or reordering hazard this rule exists to
# catch. testdata/count-index-pure-scalar holds the mirror-image fixture:
# count.index rendered as a pure, injective scalar (a template, arithmetic,
# a conditional whose own condition does not depend on count.index) in the
# same identity-bearing arguments, which is not refused.
# testdata/count-index-not-relevant holds a second mirror image: indexing
# in an argument none of this data marks as identity-bearing.

variable "rule_numbers" {
  type    = list(number)
  default = [100, 200, 300]
}

# A direct collection index: rule_number picks its value out of
# var.rule_numbers at position count.index, rather than being built from
# count.index itself.
resource "aws_network_acl_rule" "list_index" {
  count = 2

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
  count = 2

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
  count = 2

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
  count = 2

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
  count = 2

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = count.index == 0 ? 100 : 200
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}
