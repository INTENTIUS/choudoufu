# Fixture for the residue roster's registry-laggard cohort (issue #49).
# aws_codebuild_project is outside the v0 table and, per live/mapping.json
# and live/registry.json, maps to AWS::CodeBuild::Project, whose Registry
# entry ships no working handler at all.

resource "aws_codebuild_project" "build" {
  name = "example"
}
