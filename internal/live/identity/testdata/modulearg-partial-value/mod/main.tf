# list(map(string)) rather than `any`, because that is what
# terraform-aws-modules/security-group declares ingress_with_cidr_blocks as:
# the rebuilt value has to survive convert.Convert against a real type
# constraint on the way in, not merely reach the child as the caller wrote
# it. An element carrying an unknown leaf makes that conversion's own
# unification the thing most likely to lose the known siblings.
variable "rules" {
  type = list(map(string))
}

# Reads only leaves the caller wrote out, two of them, through a template so
# that the assertion is on a value neither leaf carries on its own.
resource "aws_iam_user" "literal" {
  count = length(var.rules)

  name = "${lookup(var.rules[count.index], "team", "no-team")}-${lookup(var.rules[count.index], "name", "no-name")}"
}

# Reads the one leaf that is NOT in the configuration. It has to keep
# refusing on the very same rebuilt value that answers the resource above:
# the key is present with an unknown value, so lookup() returns that unknown
# rather than the "unset" default, and an unknown is turned away.
resource "aws_iam_user" "dynamic" {
  count = length(var.rules)

  name = lookup(var.rules[count.index], "granted", "unset")
}
