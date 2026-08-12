# count derived from another resource. Cardinality is not knowable from
# configuration, and guessing it would drop or invent instances.
resource "aws_eip" "pool" {
  count = 2
}

resource "aws_cloudwatch_log_group" "per_eip" {
  count = length(aws_eip.pool)

  name = "/estate/log"
}
