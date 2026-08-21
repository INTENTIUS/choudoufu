# Issue #315's own shape: builds on module-foreach-comprehension-chase
# (#308) - a child module's for_each already proves its key set through
# #308's fix. This fixture goes one step further, into the MODULE CALL's
# own argument list, which reads each.value.<attr> off the same source
# entries whose "image" attribute reaches a data source and must stay
# unevaluated - not merely to decide which instances exist (#308's own
# shape), but to build an identity-bearing argument INSIDE the child
# module. Before #315's fix, every each.value.<attr> reference here refused
# wholesale ("Unable to use each.value.label in static context, which is
# required by module.task:var.label"), even though "label" is a plain
# literal (or a declared default) in every entry, sitting right beside the
# one genuinely unprovable attribute (image).
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
      # "label" is left unset here on purpose, so its value has to come
      # from the variable's own declared `optional(string, "default-team")`
      # default - #315's per-attribute counterpart of #308's own
      # declared-default path, needed because this entry's value as a
      # WHOLE never proves (image blocks it).
      image = data.aws_ssm_parameter.fluentbit.value
    }
    (local.container_name) = {
      create = true
      label  = "core"
      image  = "public.ecr.aws/example:latest"
    }
    disabled = {
      create = false
      label  = "unused"
      image  = "public.ecr.aws/example:latest"
    }
  }
}
