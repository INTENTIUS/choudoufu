# #184's cascade shape: the log group's own identity reads a data source
# (offline-ineligible, so identity refuses it - the corpus's dominant idiom,
# same as data-read-eligible/main.tf), and the log stream's identity, in
# turn, reads the log group's identity attribute. Offline, that second
# reference cascades into identity's own "Unresolvable identity" - a
# diagnostic identity/resolve.go raises with no reference back to the data
# read that actually caused it. Both sites must land as one eligible-read
# finding, not one eligible plus one hard "Unresolvable identity" refusal.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.primary.zone_id}"
}

resource "aws_cloudwatch_log_stream" "per_zone" {
  log_group_name = aws_cloudwatch_log_group.per_zone.name
  name            = "stream"
}
