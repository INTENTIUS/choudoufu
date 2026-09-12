# Regression fixture for GitHub issue #1063: the module-nested shape
# tools/terralith-gen actually emits (pods.tf/modules/team_pod, issue #574) -
# a `module "team_pod"` call for_each'd over a fully static set, wrapping a
# child module whose own resource names itself from a module input
# variable fed by the CALL's own arguments (prefix = "${local.name_prefix}-
# ${each.key}"). Before #1063, [moduleScope] did not exist and the module's
# own frozen var.* closure had no repetition data at all, so
# module.team_pod["pod-a"].aws_iam_policy.pod_policy[0]'s own `name`
# argument (which reads var.prefix) could never be evaluated - even though
# team_pod's own for_each collection is fully known here.

locals {
  name_prefix = "acme"
  pod_size    = 1
}

module "team_pod" {
  source = "./modules/team_pod"

  for_each = toset(["pod-a"])

  prefix   = "${local.name_prefix}-${each.key}"
  pod_size = local.pod_size
}
