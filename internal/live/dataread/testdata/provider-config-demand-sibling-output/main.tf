# GitHub issue #391's own third finding: moduleOutputLookup used to abort
# ITS WHOLE cross-module lookup - refusing every one of a child module's
# outputs, not only the one actually needed - the instant ANY ONE of them
# failed. This fixture is provider-config-demand's own shape (issue #313)
# with one addition: the child module declares a SECOND output,
# other_instance_id, that this test deliberately never supplies a live
# value for, so it stays permanently unreadable - and cluster_id, the one
# the "clusterauth" provider block actually needs, must succeed anyway.
module "child" {
  source = "./child"
}

data "aws_zone" "of_cluster" {
  name = module.child.cluster_id
}

# Reads the OTHER output - the one that genuinely never resolves - through
# ITS OWN provider block, so it is demanded exactly the way "clusterauth"
# demands of_cluster, and this test can assert the fix is precise: an
# unrelated output's own refusal must not vanish, only stop leaking onto
# its siblings.
data "aws_zone" "of_other" {
  name = module.child.other_instance_id
}

provider "clusterauth" {
  endpoint = data.aws_zone.of_cluster.zone_id
}

provider "otherauth" {
  endpoint = data.aws_zone.of_other.zone_id
}

resource "aws_cloudwatch_log_group" "marker" {
  name = "/clusters/${data.aws_zone.of_cluster.zone_id}"
}
