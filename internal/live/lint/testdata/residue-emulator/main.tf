# Fixture for the residue roster's emulator-blocked cohort (issue #49).
# aws_db_instance is outside the v0 table today: issue #26 keeps it out of
# a wiring slice because RDS only works fully against floci when the
# docker socket is mounted into the emulator container, which the harness
# does not do. (aws_ecr_repository and aws_iam_user used to be this
# fixture's example; both left the emulator-blocked roster in the second
# registry-ratified batch, #40/#44/#26 — see live/residue.go.)

resource "aws_db_instance" "database" {
  identifier = "example"
}
