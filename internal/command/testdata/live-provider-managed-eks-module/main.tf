# terraform-aws-modules/eks v20's output shape, reduced to what GitHub issue
# #1113's provider-block tests need: a count-guarded cluster, and outputs
# wrapped in try() exactly as the published module writes them.
#
# cluster_arn is a sibling no provider block names. It is here so that a
# demand which evaluated every output of the call, rather than the ones the
# block names, would show up as a read of something nothing asked for.
variable "cluster_name" {
  type = string
}

variable "create" {
  type    = bool
  default = true
}

resource "aws_eks_cluster" "this" {
  count = var.create ? 1 : 0
  name  = var.cluster_name
}

output "cluster_name" {
  value = try(aws_eks_cluster.this[0].name, "")
}

output "cluster_endpoint" {
  value = try(aws_eks_cluster.this[0].endpoint, null)
}

output "cluster_certificate_authority_data" {
  value = try(aws_eks_cluster.this[0].certificate_authority[0].data, null)
}

output "cluster_arn" {
  value = try(aws_eks_cluster.this[0].arn, null)
}
