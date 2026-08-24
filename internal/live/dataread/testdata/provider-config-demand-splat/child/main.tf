# terraform-aws-eks's own "basic" example shape (corpus-eks-basic's own
# gauntlet wall, test_plan): a count-expanded managed resource whose
# provider-assigned "id" is read back out through a legacy 0.11-style
# splat, not the plain aws_eks_cluster.this.id
# ../provider-config-demand/child/main.tf models. `element(concat(X.*.id,
# [""]), 0)` is the exact idiom terraform-aws-eks's cluster_id output uses.
resource "aws_eks_cluster" "this" {
  count = 1
  name  = "prod-cluster"
}

output "cluster_id" {
  value = element(concat(aws_eks_cluster.this.*.id, tolist([""])), 0)
}
