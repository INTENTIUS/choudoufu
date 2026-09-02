# The boundary of resolver.parentPart's record-backed branch: an attribute
# the parent's provider schema does not declare at all.
#
# The record store hydrates whatever the provider returned for random_pet,
# and this is not part of it, so there is nothing to promise and the
# "Not an identity attribute" refusal has to stand. The branch is a schema
# rule, not a licence to read any name off a record-backed parent.

resource "random_pet" "suffix" {
  length = 2
}

resource "aws_iam_group" "from_missing" {
  name = "nope-${random_pet.suffix.no_such_attribute}"
}
