# Coverage: client-named path with an ARN-shaped id (aws_ecs_cluster — the
# provider's documented import ID is the cluster name, already in config, but
# the id attribute is the cluster ARN, so only the name attribute carries the
# import identity). First slice of the survey's client-named cohort (#19).

resource "aws_ecs_cluster" "app" {
  name = "tofu-stateless-e2e-cluster"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_ecs_cluster.app"
  }
}
