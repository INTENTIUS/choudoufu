# Fixture: count.index in a position identity.LookupType marks as NOT
# identity-bearing for the resource's own type. Mirror of
# testdata/count-index, which pins the still-caught side of the same rule.
# See countIndexScopeForType (count_index.go).

# aws_vpc is server-assigned (identity.LookupType("aws_vpc").ServerAssigned):
# identity.Resolve never reads a single configuration attribute for such a
# type - it returns ClassNeedsDiscovery before ever calling identityArgs
# (internal/live/identity/resolve.go) - so no argument here, however built,
# can corrupt identity resolution. Discovery instead matches the live VPC by
# its tofu-address marker, which internal/live/stamp's addressExpr
# recomputes fresh from the resource's own address at every run, never from
# cidr_block or tags.
resource "aws_vpc" "server_assigned_non_identity" {
  count = 2

  cidr_block = "10.${count.index}.0.0/16"

  tags = {
    Name = "vpc-${count.index}"
  }
}

# aws_network_acl_rule is Components-based, but from_port and to_port are
# not among the four Attrs (network_acl_id, rule_number, protocol, egress)
# that build its import identity (table_generated.go), so count.index here
# never reaches anything identity.Resolve reads.
resource "aws_network_acl_rule" "components_non_identity_attr" {
  count = 2

  network_acl_id = "acl-0123456789abcdef0"
  rule_number = 100
  egress      = false
  protocol    = "tcp"
  rule_action = "allow"
  cidr_block  = "10.0.0.0/16"
  from_port   = count.index
  to_port     = 80 + count.index
}

# aws_route53_record's Components (zone_id, name, type) never include
# anything inside a nested block: identity's own identityArgs reads
# top-level attributes only (a top-level-only hcl.Body.PartialContent), so a
# weighted_routing_policy block's weight can never be identity-bearing,
# whatever count.index does inside it.
resource "aws_route53_record" "components_nested_block" {
  count = 2

  zone_id = "Z0123456789ABCDEFGHI"
  name    = "record.example.com"
  type    = "A"
  ttl     = 300

  weighted_routing_policy {
    weight = count.index
  }
}
