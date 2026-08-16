# The collision check compares two concrete resolutions by their identity.
# For a type whose identity is several attributes with no separator any
# schema documents - resolve.go's [TypeIdentity.IdentityObjectOnly] - the
# import ID is deliberately the empty string, and comparing THAT makes every
# instance of the type look like every other one.
#
# The three schedules below share one group and differ in the action name,
# which is exactly terraform-aws-modules/autoscaling's complete example.
# They are three distinct live objects and must resolve as three.

resource "aws_autoscaling_schedule" "this" {
  for_each = toset(["morning", "night", "go-offline-to-celebrate-new-year"])

  autoscaling_group_name = "shared-group"
  scheduled_action_name  = each.key
}

# Two blocks that really do name the same live object still have to be
# refused: the fix is about which key is compared, not about comparing less.
resource "aws_autoscaling_schedule" "duplicate_a" {
  autoscaling_group_name = "other-group"
  scheduled_action_name  = "same-action"
}

resource "aws_autoscaling_schedule" "duplicate_b" {
  autoscaling_group_name = "other-group"
  scheduled_action_name  = "same-action"
}
