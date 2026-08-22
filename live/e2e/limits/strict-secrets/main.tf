# Limits fixture: RuleStrictSecrets (GitHub issue #365).
#
# `secrets = "none"` is neither of the two settings this fork's schema
# defines. The refusal is the point, and the reason is that the two settings
# are opposites: "store" keeps the secret material a configuration generates
# the way stock OpenTofu keeps it, "refuse" keeps none of it, and a spelling
# that is neither could plausibly be read as either.
#
# Resolving it to the default would run this estate under the very setting
# the author was trying to change. Resolving it to "refuse" would refuse
# types they never asked to refuse. Neither is an answer; it is a guess with
# a plan attached. See live/LIMITATIONS.md, "strict-secrets".

terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = "none"
    }
  }
}
