# GitHub issue #221's exact repro. aws_cloudwatch_log_stream's identity has
# TWO real components (log_group_name, name - table_generated.go):
# log_group_name cascades from the log group's eligible data-source read,
# but "name" is a second, wholly independent required argument left with no
# value at all.
#
# resolveInstance (internal/live/identity/resolve.go:648-714) returns at
# the FIRST failing component of entry.Components, so "name" is never
# reached, let alone evaluated - its failure produces no diagnostic
# anywhere else in this configuration. Before #221's fix, the cascade
# fixpoint saw only that the FIRST component traced to an eligible read and
# reclassified the whole instance as "no configuration edit is needed",
# which is false: plain "tofu validate" refuses this configuration outright.
#
# This must stay a hard "Unresolvable identity" refusal - #221's
# dependentSafeToReclassify refuses to reclassify a cascade whose dependent
# type has more than one real identity component, exactly this shape.
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
