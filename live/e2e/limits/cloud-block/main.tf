# Limits fixture: RuleStateBackend, cloud form.
#
# A cloud block is a remote state backend under another name, with remote
# locking attached. Stateless mode has neither. See live/LIMITATIONS.md.

terraform {
  cloud {
    organization = "example"

    workspaces {
      name = "stateless-limits"
    }
  }
}
