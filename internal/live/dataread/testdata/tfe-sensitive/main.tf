# The same eligible tfe_outputs source as tfe-eligible, but the identity
# argument reads the whole-attribute-sensitive values map directly, with no
# nonsensitive() wrap: this is the shape that must refuse at resolution,
# carrying the remedy in its own wording.
#
# The block is aws_iam_group, not aws_cloudwatch_log_group as this fixture
# read before GitHub issue #289: aws_cloudwatch_log_group is taggable and
# enumerable, so its own marker fallback would now answer "Identity derived
# from a sensitive value" for it too - safely, since the sensitive value
# never reaches any rendered identity either way - but that is a
# different, correct outcome from what THIS fixture pins: the WORDING of a
# refusal that does still fire, specifically that it carries the
# nonsensitive() remedy. aws_iam_group carries no tags argument and stays
# outside that gate.
provider "tfe" {
  token = "static-test-token"
}

data "tfe_outputs" "app" {
  organization = "acme"
  workspace    = "prod"
}

resource "aws_iam_group" "per_workspace" {
  name = "/tfe/${data.tfe_outputs.app.values["log_group"]}"
}
