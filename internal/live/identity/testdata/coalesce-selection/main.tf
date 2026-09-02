# coalesce() in an identity argument, over a record-backed parent reached
# through a module-call argument. This is corpus-lambda-simple's own shape
# (terraform-aws-modules/terraform-aws-lambda v8.8.1): the estate names the
# function after a random_pet, and the module's IAM role, inline policy and
# log group all take their names from coalesce() chains over that one
# module input.
#
# random_pet is RECORD_ADMITTED - its id exists only in the estate's record
# store - so every identity here is a formula over it, never a literal. See
# resolver.parentPart's record-backed branch, which was already able to
# carry that across a module-call argument; what could not be carried was
# the coalesce() around it.

resource "random_pet" "suffix" {
  length = 2
}

module "child" {
  source = "./child"

  name = "${random_pet.suffix.id}-svc"
}
