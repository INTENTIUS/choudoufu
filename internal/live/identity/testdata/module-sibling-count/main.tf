# Regression fixture for the walkModule sibling-context bug: a module call
# that sorts alphabetically BEFORE a sibling with its own count, and that
# has children of its own to recurse into first. Before the fix, walking
# "a_with_children" (and its own nested module) left the resolver's
# r.mod/r.modInst pointing three levels down, with nothing restoring them
# before "b_leaf_count"'s own sibling loop iteration ran - so
# r.mod.ModuleCalls["b_leaf_count"] silently missed, its count was never
# read, and it was walked as a single unkeyed instance regardless of what
# count actually said. Found via govuk-infrastructure's opensearch
# blue/green module (issue #200): "blue_domain" sorts before
# "snapshot_bucket", and blue_domain's own subtree left module.snapshot_bucket
# walked as one phantom instance even though its count was 0.

module "a_with_children" {
  source = "./child-with-children"
}

module "b_leaf_count" {
  source = "./child-leaf"
  count  = 0
}
