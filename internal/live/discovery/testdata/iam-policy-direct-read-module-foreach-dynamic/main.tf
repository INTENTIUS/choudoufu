# Issue #1063's negative proof: team_pod's own for_each is fully static
# (same "pod-a" set as the sibling fixture), so the module still expands and
# module.team_pod["pod-a"].aws_iam_policy.pod_policy[0] is a real,
# addressable instance - but the CALL's own `prefix` argument (which
# var.prefix's value comes from) reaches a data source, which this fork's
# static-only subset never answers. [moduleVariableProblem] must refuse this
# distinctly from both the resource-level refusal and from #1063's own
# positive case, naming the module call - not the resource's own body - as
# where the problem is.

data "aws_region" "current" {}

locals {
  name_prefix = "acme"
  pod_size    = 1
}

module "team_pod" {
  source = "./modules/team_pod"

  for_each = toset(["pod-a"])

  prefix   = "${local.name_prefix}-${each.key}-${data.aws_region.current.name}"
  pod_size = local.pod_size
}
