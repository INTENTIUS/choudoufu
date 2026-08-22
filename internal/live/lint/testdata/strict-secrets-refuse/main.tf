# Clean-pass fixture: the principle turned on. HANDOFF.md's first toggle,
# "no secrets stored by the tool (secret-generating types refused, sensitive
# settable arguments never recorded)".
#
# It is clean at this layer because it is a valid, implemented setting. What
# it refuses is resources, not configurations: a secret-generating logical
# type raises RuleLogicalResource under it, which is what
# live/e2e/limits/random-password and local-sensitive-file are for.

terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = "refuse"
    }
  }
}
