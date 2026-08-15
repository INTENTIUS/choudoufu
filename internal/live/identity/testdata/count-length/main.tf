# count = length(<resource>): the bare form. The cardinality is not a
# literal in this configuration, but it is not a guess either - it is
# aws_eip.pool's own already-computed instance count, the same count
# expansionFor built for aws_eip.pool itself. This is the shape 6 of the
# 11 cyhy-amis sites in #178's count = length(<resource ref>) bucket use,
# most often a terraform_data companion that needs one instance per EC2
# instance a sibling resource creates.
resource "aws_eip" "pool" {
  count = 2
}

resource "aws_cloudwatch_log_group" "per_eip" {
  count = length(aws_eip.pool)

  name = "/estate/log-${count.index}"
}
