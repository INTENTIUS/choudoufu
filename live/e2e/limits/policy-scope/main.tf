# Limits fixture: RulePolicyScope.
#
# The maintainer's own example from GitHub issue #67, minus the scope block
# the undeclared+untagged delete quadrant needs. That quadrant reaches
# resources this configuration has never named and which carry no marker of
# this estate's, so an unscoped setting is an account-wide purge. The other
# delete quadrant, undeclared_tagged, needs no scope: the estate's own
# marker is the scope. See live/LIMITATIONS.md, "policy-scope".

terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged     = "untag"
      declared_untagged   = "converge"
      undeclared_tagged   = "keep"
      undeclared_untagged = "delete"
    }
  }
}
