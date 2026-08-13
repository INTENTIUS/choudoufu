# Fixture for RulePolicyScope: the maintainer's own example from GitHub
# issue #67, minus the scope block a delete quadrant needs. An unscoped
# account-wide purge is a lint refusal, not a default.

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
