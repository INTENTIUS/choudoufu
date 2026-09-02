# A tfe_outputs source whose provider block sets a token explicitly:
# eligibility rule 3 already proves the provider block statically
# evaluable, and the auth-surface rule finds a token argument, so the
# source is eligible - #179 stage 2's own read pipeline, not a refusal.
provider "tfe" {
  token = "static-test-token"
}

data "tfe_outputs" "app" {
  organization = "acme"
  workspace    = "prod"
}

# The tfe provider marks the whole values attribute sensitive; nonsensitive()
# is the design's documented remedy when a specific value genuinely is not
# secret, and this fixture proves the remedy actually works end to end, not
# only that its wording is offered.
resource "aws_cloudwatch_log_group" "per_workspace" {
  name = "/tfe/${nonsensitive(data.tfe_outputs.app.values["log_group"])}"
}
