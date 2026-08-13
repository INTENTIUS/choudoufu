# Fixture for the residue roster's emulator-blocked cohort (issue #49).
# aws_ecr_repository is outside the v0 table today: issue #26 kept it out
# of a wiring slice because floci's ECR emulation needs a docker daemon the
# current harness image does not carry.

resource "aws_ecr_repository" "repo" {
  name = "example"
}
