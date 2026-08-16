# Fixture: every position count.index must still be caught in, now that
# RuleCountIndex only walks arguments identity.LookupType says could feed a
# type's live identity (see countIndexScopeForType, count_index.go). All
# three resources below are aws_network_acl_rule or aws_route53_record,
# whose Components (internal/live/identity/table_generated.go) name
# rule_number/network_acl_id/protocol/egress and zone_id/name/type as
# identity-bearing - so count.index reaching one of those, however
# indirectly, is still exactly the reordering hazard this rule exists to
# catch. testdata/count-index-not-relevant holds the mirror-image fixture:
# count.index in an argument none of this data marks as identity-bearing.

# A direct reference inside an identity-bearing argument, reached through an
# arithmetic expression rather than a bare traversal.
resource "aws_network_acl_rule" "plain_arg" {
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

# The canonical case generalized: count.index interpolated into a template,
# landing in an identity-bearing attribute (name is part of
# aws_route53_record's ZONEID_NAME_TYPE import identity) rather than an
# ordinary tag.
resource "aws_route53_record" "in_identity_template" {
  count = 2

  zone_id = "Z0123456789ABCDEFGHI"
  name    = "record-${count.index}.example.com"
  type    = "A"
  ttl     = 300
  records = ["10.0.0.1"]
}

# count.index reached only through a conditional expression, still landing
# in an identity-bearing argument: a traversal walk catches this because it
# inspects the expression's referenced variables, not because the literal
# text "count.index" appears next to rule_number.
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
