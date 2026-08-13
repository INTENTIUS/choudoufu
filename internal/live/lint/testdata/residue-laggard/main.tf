# Fixture for the residue roster's registry-laggard cohort (issue #49).
# aws_codebuild_source_credential is outside the v0 table and, per
# live/mapping.json and live/registry.json, maps to
# AWS::CodeBuild::SourceCredential, whose Registry entry ships no working
# handler at all. The devtools ratification batch (issue #65) rejected this
# type outright rather than admitting it (see
# internal/live/identity/table.go), so it remains a genuine registry
# laggard, unlike its sibling aws_codebuild_project, which that same batch
# admitted by correcting row-gen's proposal against the provider's own
# documented import behaviour - this fixture used aws_codebuild_project
# before that batch and moved to a still-unadmitted CodeBuild type so this
# test keeps exercising the registry-laggard cohort rather than tripping
# over the admission table's own growth.

resource "aws_codebuild_source_credential" "github" {
  auth_type   = "PERSONAL_ACCESS_TOKEN"
  server_type = "GITHUB"
  token       = "example"
}
