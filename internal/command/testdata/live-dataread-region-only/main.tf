# The control for the data-read boundary: an ordinary cloud data source in an
# identity-bearing position. Nothing here can run locally, stock OpenTofu
# plans it without complaint, and the phase must go on reading it.
data "aws_region" "current" {}

resource "aws_cloudwatch_log_group" "regional" {
  name = "/logs/${data.aws_region.current.name}"
}
