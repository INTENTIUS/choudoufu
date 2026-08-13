# Fixture for RulePolicyScope: a scope block that names no service, type or
# region does not narrow anything, so it does not satisfy the delete
# quadrant's scope requirement any more than a missing scope block would.

terraform {
  live {
    estate = "my-estate"
    policy {
      undeclared_untagged = "delete"
      scope {}
    }
  }
}
