# Fixture for RuleStateBackend, backend form.

terraform {
  backend "local" {
    path = "terraform.tfstate"
  }
}
