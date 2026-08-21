# The condition GitHub issue #346's widening did NOT relax. test_synth_parent
# has no DefaultTable row, so its entry is whatever SynthesizeTypeIdentity
# reads off the provider's identity schema - here a Required "name", which is
# enough to admit it and resolve it. Deferring a SECOND value to that entry
# would stack an inference on an inference, so `endpoint` - a plain Computed
# attribute the schema does declare - stays refused.

resource "test_synth_parent" "p" {
  name = "primary"
}

resource "aws_iam_group" "by_endpoint" {
  name = "logs-${test_synth_parent.p.endpoint}"
}
