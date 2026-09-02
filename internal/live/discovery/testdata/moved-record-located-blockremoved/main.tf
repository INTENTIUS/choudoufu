# gauntlet:destroy-order fixture: the SAME rename as
# ../moved-record-located, but the renamed block (aws_iam_role_policy.inline)
# has since been deleted too - corpus-security-group-complete's day2_remove
# shape exactly: Part D renames module.postgresql to module.postgresql_renamed
# with a `moved` block, then Part E deletes module.postgresql_renamed's own
# block entirely while the `moved` block stays in main.tf. Neither address is
# declared here any more, so the "declared instance's own moved aliases" loop
# in recordOrphanReadSweep has nothing to mark known, and the record - still
# filed under the OLD address, since a bare `moved` block never re-keys it -
# has to be found and translated forward to the NEW address.

moved {
  from = aws_iam_role_policy.inline_old
  to   = aws_iam_role_policy.inline
}
