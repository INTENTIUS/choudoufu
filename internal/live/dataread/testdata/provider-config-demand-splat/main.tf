# The same three-hop shape ../provider-config-demand/main.tf exercises -
# a PROVIDER BLOCK's own argument reads a data source, whose own argument
# crosses a child module's output, whose own expression reads a managed
# resource attribute no literal argument covers - except the child
# module's own output reads that attribute through a legacy
# `resource.*.attr` splat over a count-expanded resource
# (./child/main.tf), the exact shape corpus-eks-basic's real
# terraform-aws-eks module uses for its own cluster_id output.
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
