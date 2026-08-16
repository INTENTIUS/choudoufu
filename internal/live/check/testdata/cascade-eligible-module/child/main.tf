# Same cascade shape as ../../cascade-eligible/main.tf, but inside a child
# module: identity.StaticIdentifier.String() prefixes NeededBy with
# "module.child:" while the resource-instance address it wraps already
# carries "module.child." from addrs.AbsResourceInstance.String() - the two
# module prefixes are spelled differently (":" vs ".") and the second is
# redundant. This fixture pins that the address recovered from NeededBy
# still matches the cascade diagnostic's own parent text exactly.
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
