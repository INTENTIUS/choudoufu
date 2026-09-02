# Fixture for RuleStateBackend, cloud form.

terraform {
  cloud {
    organization = "example"

    workspaces {
      name = "stateless-lint"
    }
  }
}
