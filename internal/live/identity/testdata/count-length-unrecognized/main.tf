# length() of a for expression over a resource, not a bare reference to the
# resource itself. This is #178's separate "for/splat over all instances
# into a list" bucket, which needs its own list-shaped Formula machinery -
# this rule must keep refusing it honestly rather than guess just because
# length() and a resource reference both appear somewhere inside the count
# expression.
resource "aws_eip" "pool" {
  count = 2
}

resource "aws_cloudwatch_log_group" "per_eip" {
  count = length([for e in aws_eip.pool : e.id])

  name = "/estate/log"
}
