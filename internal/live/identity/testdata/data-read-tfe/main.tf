# A tfe_outputs value feeding an identity argument, #179 stage 2's flavor.
# The DataResults seam identity.ResolveWith consumes is generic over
# resource type - resolution needs no change for a cross-stack source, only
# the data-read phase's eligibility rule differs (internal/live/dataread).
data "tfe_outputs" "app" {
  organization = "acme"
  workspace    = "prod"
}

resource "aws_cloudwatch_log_group" "per_workspace" {
  name = "/tfe/${nonsensitive(data.tfe_outputs.app.values["log_group"])}"
}
