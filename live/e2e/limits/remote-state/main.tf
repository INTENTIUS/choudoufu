# Limits fixture: RuleRemoteState.
#
# terraform_remote_state reads a state file, and stateless mode has no state
# to read. See live/LIMITATIONS.md.

data "terraform_remote_state" "network" {
  backend = "local"

  config = {
    path = "../network/terraform.tfstate"
  }
}
