# Coverage: marker path (aws_kms_key — KMS mints the key ID at create time
# and nothing in this block names it, so the tofu-address tag is the only way
# back to it). First slice of the survey's marker cohort (#20).
#
# deletion_window_in_days is deliberately left at its default rather than
# set. KMS does not serve it back on a read, so under markers — where prior
# state is an import plus a read, with no file remembering what was written —
# setting it would make every plan propose the same in-place update forever.
# Same shape as aws_security_group's revoke_rules_on_delete in
# stateless/LIMITATIONS.md, except that this one is avoidable by not writing
# the argument.

resource "aws_kms_key" "main" {
  description = "estate fixture key"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_kms_key.main"
  }
}
