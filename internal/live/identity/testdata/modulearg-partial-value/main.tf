# modulearg-partial's key-set half, applied to an identity ARGUMENT's value.
#
# The caller writes a composite module argument whose skeleton is entirely
# literal and one of whose leaves is not. modulearg-partial pins that the
# child's count and for_each still name their instances; this pins what one
# of those instances' identity-bearing arguments says.
#
# terraform-aws-modules/security-group's own ingress_with_cidr_blocks is the
# shape verbatim - from_port, to_port and protocol written out by the caller,
# cidr_blocks reading a sibling module's output - and before the identity
# retry all four refused together.
#
# The two resources in ./mod are the two directions this has to get right:
# one whose identity reads only the leaves the caller wrote out, and one
# whose identity reads the leaf that is not in the configuration at all.
resource "aws_iam_role" "r" {
  name = "the-role"
}

module "u" {
  source = "./mod"

  rules = [
    {
      name    = "alpha"
      team    = "platform"
      granted = aws_iam_role.r.arn
    },
  ]
}
