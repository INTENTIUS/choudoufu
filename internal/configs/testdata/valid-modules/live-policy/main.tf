// The maintainer's exact example from GitHub issue #67's Design section.
terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged     = "untag"     # remove the tag; source owns it now
      declared_untagged   = "converge"  # ordinary management ("intended state")
      undeclared_tagged   = "keep"      # the tag protects it
      undeclared_untagged = "delete"    # account-scope reconciliation
    }
  }
}
