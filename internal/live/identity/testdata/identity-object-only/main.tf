# A synthetic stand-in for the identity-object-only shape ([TypeIdentity.IdentityObjectOnly]):
# a type whose identity is several client-named attributes with no separator
# any schema documents to join them with, so [classify] leaves the import ID
# empty on purpose and the collision key has to compare the attribute values
# instead. aws_autoscaling_schedule used to be this shape and is the type the
# corpus originally hit the bug on (see collisionkey_test.go), but issue
# #245's composite-bucket ratification batch gave it a real "/"-joined
# import ID straight from the provider's own Import section, so it no longer
# exercises this path. This fixture keeps the regression alive under a type
# name no ratification can ever claim.
#
# Two blocks that name the same group and action are the same live object
# and still have to be refused: the fix this file guards is about which key
# the collision check compares, not about comparing less.
resource "aws_test_identity_object_only" "duplicate_a" {
  group_name  = "other-group"
  action_name = "same-action"
}

resource "aws_test_identity_object_only" "duplicate_b" {
  group_name  = "other-group"
  action_name = "same-action"
}
