# GitHub issue #221's exact repro. aws_cloudwatch_log_stream's identity has
# TWO real components (log_group_name, name - table_generated.go):
# log_group_name cascades from the log group's eligible data-source read,
# but "name" is a second, wholly independent required argument left with no
# value at all.
#
# Before #221's fix, resolveInstance (internal/live/identity/resolve.go)
# returned at the FIRST failing component of entry.Components, so "name"
# was never reached, let alone evaluated - its failure produced no
# diagnostic anywhere else in this configuration, and the cascade fixpoint
# saw only that the FIRST component traced to an eligible read and
# reclassified the whole instance as "no configuration edit is needed",
# which was false: plain "tofu validate" refuses this configuration
# outright.
#
# After #221's proper fix, resolveInstance evaluates every component, so
# "name"'s own failure now raises its own "Identity argument not set"
# diagnostic - and internal/live/check's fixpoint refuses to reclassify the
# log_group_name cascade for the same instance, because that sibling
# failure is not itself eligible-traceable. Both stay hard-refused; see
# TestAnalyzeDoesNotReclassifyMultiComponentCascade.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_cloudwatch_log_group" "p" {
  name = "/zones/${data.aws_route53_zone.primary.zone_id}"
}

resource "aws_cloudwatch_log_stream" "child" {
  log_group_name = aws_cloudwatch_log_group.p.name
  # "name" deliberately omitted - required, no value at all.
}
