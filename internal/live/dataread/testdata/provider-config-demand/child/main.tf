resource "aws_eks_cluster" "this" {
  name = "prod-cluster"
}

output "cluster_id" {
  value = aws_eks_cluster.this.id
}
