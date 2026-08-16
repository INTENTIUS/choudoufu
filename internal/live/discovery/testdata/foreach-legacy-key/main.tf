# Fixture for the issue #178 migration test: one for_each subnet keyed by a
# string containing "@" - already legal before #178 admitted "." and ":",
# and stamped unescaped, so it is the one shape where a marker a prior run
# wrote (aws_subnet.this:at@sign) differs from what this run would stamp
# fresh (aws_subnet.this:at@@sign). See discovery_migration_test.go.
resource "aws_subnet" "this" {
  for_each = { "at@sign" = "10.99.0.0/24" }

  cidr_block = each.value
}
