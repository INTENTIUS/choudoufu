# The corpus's dominant data-source idiom, reduced: a data source's
# attribute in an identity argument, with everything else clean. The one
# finding must be the data-read pass's eligible-read, and the estate lands
# on the data-read-eligible rung.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.primary.zone_id}"
}
