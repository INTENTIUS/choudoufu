variable "create" {
  type    = bool
  default = true
}

# The declared type is the point of this fixture. terraform-aws-modules
# declares every "map of definitions to create" argument this way, and a
# declared type is what makes the caller's own constructor stop being the
# element: prepareFinalInputVariableValue applies the optional() defaults and
# converts before anything in here reads anything.
variable "attachments" {
  type = map(object({
    identifier = string
    type       = optional(string, "elbv2")
  }))
  default = null
}

# The same shape with the selected attribute declared a NUMBER. A conversion
# to a number is not the identity function on a string, so nothing may render
# the caller's own expression here - see [declaredAttrString].
variable "numeric" {
  type = map(object({
    identifier = number
  }))
  default = null
}

# The negative control that keeps the deferred selection from being a licence:
# the leaf it lands on is a plain attribute of a server-assigned resource, not
# an identity attribute of it, so [resolver.parentPart] refuses it exactly as
# it refuses a direct reference to the same thing.
variable "other" {
  type = map(object({
    identifier = string
  }))
  default = null
}

variable "policies" {
  type = map(object({
    name   = optional(string)
    target = optional(string)
  }))
  default = null
}

# The guard clause, verbatim in shape from terraform-aws-modules/autoscaling:
# the for_each source is not the variable but a conditional over it.
resource "aws_autoscaling_traffic_source_attachment" "this" {
  for_each = var.create && var.attachments != null ? var.attachments : {}

  autoscaling_group_name = "asg-fixed"

  traffic_source {
    identifier = each.value.identifier
    type       = try(each.value.type, "elbv2")
  }
}

resource "aws_autoscaling_traffic_source_attachment" "numeric" {
  for_each = var.create && var.numeric != null ? var.numeric : {}

  autoscaling_group_name = "asg-numeric"

  traffic_source {
    identifier = each.value.identifier
    type       = "elbv2"
  }
}

resource "aws_autoscaling_traffic_source_attachment" "other" {
  for_each = var.create && var.other != null ? var.other : {}

  autoscaling_group_name = "asg-other"

  traffic_source {
    identifier = each.value.identifier
    type       = "elbv2"
  }
}

# The control that keeps the deferred selection subordinate to the value: the
# element's `name` is absent, so the declared type makes it null inside here,
# and coalesce() therefore takes each.key. Nothing may re-route that through
# the caller's constructor, which has no `name` in it at all.
resource "aws_autoscaling_policy" "this" {
  for_each = var.create && var.policies != null ? var.policies : {}

  autoscaling_group_name = "asg-fixed"
  name                   = try(coalesce(each.value.name, each.key), "")
}
