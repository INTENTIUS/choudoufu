# GitHub issue #580, case A: the shape stock OpenTofu plans without a word
# and this pass refused. The module call is for_each-expanded and the
# identity-bearing argument reads each.key at the CALL SITE, so every module
# instance is passed a different prefix and the eight rendered names are
# globally distinct - "tl-pod-a-team-0000-role" through
# "tl-pod-b-team-0003-role", exactly what stock names them.
#
# Before module_instance_eval.go, the child module's frozen var.* closure
# (ModuleCall.decodeStaticVariables) evaluated `prefix` with no repetition
# data at all, static_scope.go refused each.key in a static context,
# var.prefix came back unknown, and the count.index domain reported "cannot
# prove". The refusal text then asserted something false of this
# configuration: that var.prefix is "an input variable with no value".
#
# tools/terralith-gen emits this exact shape at pod_size > 1, which is why
# the terralith-scale estate could not plan above scale 1.

locals {
  name_prefix = "tl"
  pod_size    = 4
}

module "m" {
  source   = "./m"
  for_each = toset(["pod-a", "pod-b"])

  prefix   = "${local.name_prefix}-${each.key}"
  pod_size = local.pod_size
}
