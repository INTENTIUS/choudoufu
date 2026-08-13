# Fixture for RulePolicyThreshold: threshold = 0 decodes fine (it is a
# non-negative literal) but is not a positive first-run guard.

terraform {
  live {
    estate = "my-estate"
    policy {
      undeclared_untagged = "delete"
      threshold            = 0
      scope {
        services = ["ec2"]
      }
    }
  }
}
