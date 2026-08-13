# Fixture for the residue roster's emulator-blocked cohort (issue #49).
# aws_ssm_document is outside the v0 table today: floci answers
# ssm:CreateDocument with UnsupportedOperation, so no SSM document can be
# created against the emulator at all (choudoufu#26). (This fixture's
# example used to be aws_cloudfront_distribution, which left
# live/residue.go's EmulatorBlocked roster entirely once the Route53
# remainder/CloudFront batch — issue #65's ratification campaign — found
# the pinned floci image's CloudFront lifecycle fix (lex00/floci#29)
# already landed; aws_instance was considered as this fixture's
# replacement but is reserved elsewhere as the "surveyed but unadmitted,
# in no cohort" example — see live/residue.go.)

resource "aws_ssm_document" "runbook" {
  name          = "example"
  document_type = "Command"

  content = jsonencode({
    schemaVersion = "2.2"
    description   = "example"
    mainSteps     = []
  })
}
