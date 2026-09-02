# #213: an identity argument reads local.username, and local.username's OWN
# definition (elsewhere, in the locals block below) references each.value
# directly. Before the seam (configs.StaticEvaluator.WithRepetitionData),
# GetLocalValue's nested scope had no channel to receive this instance's
# each.value and refused every such site as "Dynamic value in static
# context" - the top-level filter in resolve.go's evalPure only ever
# covered a DIRECT each/count reference in the resource's own arguments,
# never one reached through a local.
#
# Parity note (recorded here because it is easy to get backwards): real
# Terraform refuses each.value inside a bare locals block outright -
# "each.value cannot be used in this context" - confirmed empirically
# against `terraform plan`, and independently confirmed by this repo's own
# internal/tofu/node_local.go: nodeExpandLocal.DynamicExpand only expands a
# local by MODULE instance (expander.ExpandModule), never by a resource's
# own for_each/count. A real, previously-`apply`'d estate can therefore
# never contain this exact shape - it would have failed the user's own
# `terraform plan` before choudoufu ever saw it. This fixture pins the
# resolver's mechanism exactly as #213 specifies it; TestLocalAttrRepetition
# is the two-instances-never-share-a-value check at the identity-resolver
# layer, complementing internal/configs' TestStaticEvaluator_WithRepetitionData_ThroughLocal.

resource "aws_iam_user" "team" {
  for_each = toset(["alice", "bob"])

  name = local.username
}

locals {
  username = "user-${each.value}"
}
