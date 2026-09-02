# A data source's value driving for_each: the read result's keys become
# instance keys, so the aggregate the evaluator answers with has to be
# shaped exactly as the plan-time evaluator would shape it.
data "aws_availability_zones" "here" {}

resource "aws_cloudwatch_log_group" "per_az" {
  for_each = toset(data.aws_availability_zones.here.names)

  name = "/az/${each.value}"
}
