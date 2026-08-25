# corpus-dynamodb-table-basic's own greenfield shape (GitHub issue #388's
# plan-node seam, and this same package's static half): a child reads a
# NON-identity attribute of a parent whose own identity is not concrete yet
# either - it is a formula over a record-backed grandparent.
#
# random_pet is RECORD_ADMITTED (identity.ClassRecordBacked). aws_dynamodb_
# table's own identity (its `name`) is a formula over random_pet.suffix.id,
# so aws_dynamodb_table.this resolves ClassParentDerived, not concrete - and
# used to be the one class resolver.parentPart's deferrable check did not
# cover. aws_dynamodb_resource_policy's own identity is `resource_arn`,
# which reads the table's `arn` - a real, Computed attribute of
# aws_dynamodb_table that is not one of its IdentityAttrs (only "id" and
# "name" are).

resource "random_pet" "suffix" {
  length = 2
}

resource "aws_dynamodb_table" "this" {
  name = "my-table-${random_pet.suffix.id}"
}

resource "aws_dynamodb_resource_policy" "this" {
  resource_arn = aws_dynamodb_table.this.arn
}
