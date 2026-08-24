resource "aws_eks_cluster" "this" {
  name = "prod-cluster"
}

resource "aws_instance" "other" {
  ami = "ami-unrelated"
}

output "cluster_id" {
  value = aws_eks_cluster.this.id
}

# Deliberately unrelated to cluster_id: no test in this package ever
# supplies aws_instance.other's id through LiveManagedResults, so this
# output never resolves. Before this issue's fix, its own refusal aborted
# moduleOutputLookup's WHOLE returned object - cluster_id included - which
# is exactly what TestModuleOutputSiblingRefusalDoesNotBlockAnAnswerable
# Output proves no longer happens.
output "other_instance_id" {
  value = aws_instance.other.id
}

# corpus-eks-basic's own gauntlet wall (test_plan, #391's continuation): a
# THIRD sibling output, deliberately unrelated to cluster_id in every way
# other_instance_id above is not - it goes through a DATA SOURCE dependency
# rather than a managed-resource attribute, so it exercises analyze.go's
# lookupFactory/classify dependency-TRACKING side (Source.Deps, which
# decides eligibility) rather than moduleOutputLookup's own value-level
# unanswered/unprojectedAttr mechanism the two tests above already cover.
#
# aws_zone.poison always refuses classification outright (rule 4: naming a
# managed resource in depends_on defers the read until that resource is
# planned), regardless of any LiveManagedResults ever supplied. Before this
# fix, evaluating poison_output during a call to module.child.cluster_id
# recorded data.aws_zone.poison as a DEPENDENCY of whichever OUTER source
# was asking - data.aws_zone.of_cluster, in this fixture - through the one
# `record` closure moduleOutputLookup's per-output loop shares across every
# output it evaluates while answering that one call, not scoped to the
# output actually read. of_cluster's own argument never names poison_output
# at all.
resource "aws_instance" "gatekeeper" {
  ami = "ami-gatekeeper"
}

data "aws_zone" "poison" {
  name       = "static-poison"
  depends_on = [aws_instance.gatekeeper]
}

output "poison_output" {
  value = data.aws_zone.poison.name
}
