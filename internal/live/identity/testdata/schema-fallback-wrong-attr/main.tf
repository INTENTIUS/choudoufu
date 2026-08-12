# A synthesized entry hands out exactly one attribute as an identity source:
# the one the provider's identity schema names. Reading the parent's id
# instead is the aws_route mistake - a provider-synthesized value that is not
# the import identity - and it is refused rather than assumed.

resource "aws_thing" "one" {
  name = "alpha"
}

resource "aws_child_thing" "three" {
  parent = aws_thing.one.id
}
