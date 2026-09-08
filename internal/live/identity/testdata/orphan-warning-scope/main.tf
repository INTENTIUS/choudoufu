# Two types, one per path through resolver.lookupType's schema side
# (GitHub issue #980).
#
# aws_iam_role HAS a row in the generated admission table, and the fake
# schema the test hands in reproduces that row, so the schema-first path
# (ruling 2, #387) uses the synthesized entry instead of the row. The type
# is still in the table, so the estate-wide sweep still lists it.
#
# aws_thing has no row at all and is admitted only because the provider's
# identity schema settles it, which is issue #107's condition: the sweep
# draws its universe from the table's keys and cannot see it.

resource "aws_iam_role" "example" {
  name = "example-role"
}

resource "aws_thing" "one" {
  name = "alpha"
}
