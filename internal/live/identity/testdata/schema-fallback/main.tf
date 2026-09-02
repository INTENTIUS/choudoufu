# Three resource types with no entry in the v0 identity table, all of which
# the provider's own resource identity schema describes completely enough for
# resolution to proceed without one.
#
# aws_thing's identity attribute is a required argument, so the schemas settle
# it alone. aws_named_thing's is Optional+Computed, which is the shape
# aws_s3_bucket's bucket and aws_vpc's id share, and this block naming it is
# what separates the two. aws_child_thing reads its parent through the one
# attribute a synthesized entry hands out.

resource "aws_thing" "one" {
  name = "alpha"
}

resource "aws_named_thing" "two" {
  label = "beta"
}

resource "aws_child_thing" "three" {
  parent = aws_thing.one.name
}
