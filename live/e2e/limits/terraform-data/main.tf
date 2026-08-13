# Limits fixture: RuleLogicalResource, terraform_data.
#
# terraform_data shares null_resource's whole story - its id and output are
# minted and remembered, not observed from anything live - but shares no
# type-name prefix with it or with any other logical type. GitHub issue #73's
# audit found it missing from the old prefix list entirely, so it used to
# fall through to the generic unadmitted-type refusal instead of this one.
# This fixture pins that gap closed: internal/live/lint/logical_type.go
# admits it to the per-type table by exact name. See live/LIMITATIONS.md.

resource "terraform_data" "trigger" {
  input = "value"
}
