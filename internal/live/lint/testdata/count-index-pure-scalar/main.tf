# Fixture: count.index rendered as a pure, injective scalar - a template,
# arithmetic, a conditional whose own condition does not depend on
# count.index, or a bare argument - landing in an identity-bearing
# argument. None of these index into a collection (no *hclsyntax.IndexExpr's
# Key position is ever reached), call one of countIndexAccessorFunctions, or
# select a conditional branch by count.index, so none are refused: see
# unsafeCountIndexRanges (count_index.go) and doc.go's "Scope of the
# count.index rule". A plain scalar count never renumbers on scale-down -
# OpenTofu always retires the highest index first - so a value built purely
# from count.index names the same instance on every run regardless of how
# many higher indices come and go, and never collides with a sibling
# index's value. Mirror of testdata/count-index, which pins the
# still-caught indexing, accessor-function, and branch-selecting-conditional
# shapes of the same rule using the same two resource types.

# Arithmetic on the index itself, landing directly in rule_number.
resource "aws_network_acl_rule" "arithmetic" {
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

# count.index interpolated into a template, landing in an identity-bearing
# attribute (name is part of aws_route53_record's ZONEID_NAME_TYPE import
# identity) rather than an ordinary tag.
resource "aws_route53_record" "template" {
  count = 2

  zone_id = "Z0123456789ABCDEFGHI"
  name    = "record-${count.index}.example.com"
  type    = "A"
  ttl     = 300
  records = ["10.0.0.1"]
}

# A conditional whose branches each apply arithmetic to count.index, but
# whose own condition does not depend on count.index at all (it is the same
# for every instance) - so every instance takes the same branch, and the
# whole expression reduces to one injective arithmetic transform, not a
# branch selection keyed on the index. Contrast testdata/count-index's
# "conditional" resource, whose condition does depend on count.index and
# stays refused for exactly that reason.
resource "aws_network_acl_rule" "conditional_on_other_value" {
  count = 2

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = var.is_primary ? 100 + count.index : 200 + count.index
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}

variable "is_primary" {
  type    = bool
  default = true
}

# A bare argument: count.index used with no wrapping expression at all.
resource "aws_network_acl_rule" "bare" {
  count = 2

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = count.index
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = 80
  to_port     = 80
}
