# A tfe_outputs source with no token argument, no TFE_TOKEN, and (per the
# test's HOME override) no CLI credentials entry: the auth-surface rule
# refuses this offline, before any provider call is attempted.
data "tfe_outputs" "app" {
  organization = "acme"
  workspace    = "prod"
}

resource "aws_cloudwatch_log_group" "per_workspace" {
  name = "/tfe/${data.tfe_outputs.app.values["log_group"]}"
}
