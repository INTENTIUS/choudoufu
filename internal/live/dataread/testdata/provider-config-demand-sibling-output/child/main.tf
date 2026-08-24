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
