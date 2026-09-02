# Two resources of a type whose identity is just its name
# (aws_cloudwatch_log_group), declared with the identical name but a
# different `region` argument each - the shape govuk-infrastructure's
# chat estate actually ships (issue #200): a deliberate multi-region
# redundancy pair, not a collision. checkCollisions must not flag "dublin"
# and "london" against each other, because they are not the same live
# object; it must still flag "dublin" against "dublin_again", which shares
# both the name and the region.

resource "aws_cloudwatch_log_group" "dublin" {
  name   = "/aws/bedrock"
  region = "eu-west-1"
}

resource "aws_cloudwatch_log_group" "london" {
  name   = "/aws/bedrock"
  region = "eu-west-2"
}

resource "aws_cloudwatch_log_group" "dublin_again" {
  name   = "/aws/bedrock"
  region = "eu-west-1"
}
