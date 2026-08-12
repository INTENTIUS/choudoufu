# Limits fixture: RuleStateBackend, backend form.
#
# A backend configures where authoritative state is stored and locked;
# stateless mode has no state file to store. See stateless/LIMITATIONS.md.

terraform {
  backend "local" {
    path = "terraform.tfstate"
  }
}
