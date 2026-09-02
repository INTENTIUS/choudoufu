# The module-output hop (configs.StaticEvaluator.WithModuleOutputResults):
# a data source's own argument reads a CHILD module's output
# (module.child.cluster_id), and that output's own expression reads its
# module's managed resource - the exact shape data.aws_eks_cluster.cluster's
# "name = module.eks.cluster_id" is, reduced to a synthetic provider so this
# test needs no cloud.
module "child" {
  source = "./child"
}

data "test_zone" "of_cluster" {
  name = module.child.cluster_id
}

resource "aws_cloudwatch_log_group" "per_cluster" {
  name = "/clusters/${data.test_zone.of_cluster.zone_id}"
}
