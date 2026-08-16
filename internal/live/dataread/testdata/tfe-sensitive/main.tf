# The same eligible tfe_outputs source as tfe-eligible, but the identity
# argument reads the whole-attribute-sensitive values map directly, with no
# nonsensitive() wrap: this is the shape that must refuse at resolution,
# carrying the remedy in its own wording.
provider "tfe" {
  token = "static-test-token"
}

data "tfe_outputs" "app" {
  organization = "acme"
  workspace    = "prod"
}

resource "aws_cloudwatch_log_group" "per_workspace" {
  name = "/tfe/${data.tfe_outputs.app.values["log_group"]}"
}
