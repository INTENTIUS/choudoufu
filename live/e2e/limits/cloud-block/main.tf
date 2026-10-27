# Limits fixture: RuleStateBackend, cloud form.
#
# A cloud block is a remote state backend under another name, with remote
# locking attached. With no live block the run reads no state through it,
# so this is a warning; beside a live block it stays and is ignored (#2213).
# See live/LIMITATIONS.md, "cloud-block".

terraform {
  cloud {
    organization = "example"

    workspaces {
      name = "live-limits"
    }
  }
}
