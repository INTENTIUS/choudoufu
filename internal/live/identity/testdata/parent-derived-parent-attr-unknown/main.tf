# The boundary of resolver.parentPart's parent-derived branch: an attribute
# the parent's provider schema does not declare at all.
#
# aws_dynamodb_table.this is ClassParentDerived (its `name` is a formula
# over random_pet.suffix.id), and its whole provider object is read live
# before the child's formula renders - but "no_such_attribute" is not part
# of what the (fake) provider serves, so there is nothing to promise and
# the "Not an identity attribute" refusal has to stand.

resource "random_pet" "suffix" {
  length = 2
}

resource "aws_dynamodb_table" "this" {
  name = "my-table-${random_pet.suffix.id}"
}

resource "aws_dynamodb_resource_policy" "this" {
  resource_arn = aws_dynamodb_table.this.no_such_attribute
}
