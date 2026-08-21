# Issue #308's own shape: terraform-aws-modules/terraform-aws-ecs's
# "service" module wraps a for_each'd child module call on a
# for-comprehension, `{ for k, v in var.container_definitions : k => v if
# local.create_task_definition && v.create }`, ranging over a bare
# var.container_definitions reference whose actual object literal - with
# provably static keys - lives one module-call boundary up, right here.
#
# "fluent-bit" never sets "create" at all, so its value comes from the
# variable's own declared `optional(bool, true)` default (wrapper/main.tf).
# "app" sets create = true explicitly. "disabled" sets create = false and
# must not produce an instance at all. Every entry's "image" reaches a data
# source or is otherwise irrelevant to the filter, and must never be
# evaluated by the for_each proof - only "task"'s own family name (each.key)
# is used downstream, never each.value.
data "aws_ssm_parameter" "fluentbit" {
  name = "/aws/service/aws-for-fluent-bit/stable"
}

locals {
  container_name = "app"
}

module "wrapper" {
  source = "./wrapper"

  container_definitions = {
    fluent-bit = {
      image = data.aws_ssm_parameter.fluentbit.value
    }
    (local.container_name) = {
      create = true
      image  = "public.ecr.aws/example:latest"
    }
    disabled = {
      create = false
      image  = "public.ecr.aws/example:latest"
    }
  }
}
