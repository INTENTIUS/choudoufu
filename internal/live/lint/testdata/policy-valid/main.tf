# Clean-pass fixture: every check checkLivePolicy runs should pass. The
# delete quadrant carries a scope block, and every verb is valid for its
# quadrant.

terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged     = "untag"
      declared_untagged   = "converge"
      undeclared_tagged   = "keep"
      undeclared_untagged = "delete"

      scope {
        services = ["ec2"]
      }
    }
  }
}
