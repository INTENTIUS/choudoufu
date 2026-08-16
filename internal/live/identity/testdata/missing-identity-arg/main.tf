# No name argument at all: nothing to build an identity from. aws_iam_group's
# name is Required and carries none of the auto-generation conventions
# (Component.Default, Component.ServerAssignedIfAbsent, a *_prefix sibling)
# that would otherwise turn this into a ClassNeedsDiscovery resolution - see
# #190. Deliberately not aws_s3_bucket any more: its own `bucket` argument
# gained ServerAssignedIfAbsent once #190 taught the table the provider's own
# "If omitted, Terraform will assign a random, unique name" convention, so an
# omitted bucket now resolves instead of refusing.
resource "aws_iam_group" "admins" {
  path = "/"
}
