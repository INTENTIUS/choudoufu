# The full provider-configuration dependency-order shape (issue #313,
# corpus-eks-basic's boundary), reduced to a synthetic provider so this test
# needs no cloud: a PROVIDER BLOCK's own argument reads a data source, whose
# own argument crosses into a child module's output, whose own expression
# reads a managed resource attribute the block does not literally set - the
# same three hops "provider.kubernetes { host = data.aws_eks_cluster.
# cluster.endpoint }" needs, with data.aws_eks_cluster.cluster's own
# "name = module.eks.cluster_id" and module.eks's own
# "aws_eks_cluster.this[0].id" in between.
module "child" {
  source = "./child"
}

data "aws_zone" "of_cluster" {
  name = module.child.cluster_id
}

provider "clusterauth" {
  endpoint = data.aws_zone.of_cluster.zone_id
}

resource "aws_cloudwatch_log_group" "marker" {
  name = "/clusters/${data.aws_zone.of_cluster.zone_id}"
}
