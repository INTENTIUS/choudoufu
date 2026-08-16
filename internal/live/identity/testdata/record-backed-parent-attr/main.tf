# A record-backed parent read by a cloud-backed child's identity argument.
#
# random_pet, terraform_data and null_resource are all RECORD_ADMITTED
# (identity.ClassRecordBacked): their whole object lives in the estate's
# record store, so none of them has a cloud identity and none carries any
# IdentityAttrs. Reading one of their attributes is still perfectly
# answerable - the projection hydrates the parent from the record before any
# child's formula renders - which is what resolver.parentPart's
# record-backed branch is for.
#
# aws_cloudwatch_log_group is the child throughout: a real DefaultTable row
# whose whole identity is its name argument, with no cloud scope in it, so
# nothing here depends on an account or a region and the only thing under
# test is the parent's class.

resource "random_pet" "suffix" {
  length = 2
}

resource "terraform_data" "seed" {
  input = "static"
}

resource "null_resource" "gate" {
  triggers = {
    always = "yes"
  }
}

# Computed attribute of a record-backed parent: the case #220's
# siblingLiteralExpr deliberately refuses and only the record store can
# answer.
resource "aws_cloudwatch_log_group" "from_pet" {
  name = "svc-${random_pet.suffix.id}"
}

resource "aws_cloudwatch_log_group" "from_data" {
  name = "seeded-${terraform_data.seed.output}"
}

resource "aws_cloudwatch_log_group" "from_null" {
  name = "gated-${null_resource.gate.id}"
}

# Two record-backed parents in one identity, to prove the branch composes
# rather than only ever supplying a whole identity on its own.
resource "aws_cloudwatch_log_group" "from_both" {
  name = "${random_pet.suffix.id}-${null_resource.gate.id}"
}
