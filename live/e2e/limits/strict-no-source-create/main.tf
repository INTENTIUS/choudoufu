# Limits fixture: RuleStrictNoSourceCreate (GitHub issue #365, ruling 4;
# rfc/20260823-foundation-order-ruling.md).
#
# `no_source_create = "maybe"` is neither of the two settings this fork's
# schema defines. The refusal is the point, for the same reason
# strict-secrets's is: the two settings are opposites ("refuse", the
# default, leaves a no-source instance blocked and named; "create" selects
# stock's own behavior of planning a create instead), so a spelling that is
# neither is a question this package cannot answer without guessing which
# way the author meant to move. See live/LIMITATIONS.md,
# "strict-no-source-create".

terraform {
  live {
    estate = "my-estate"
    strict {
      no_source_create = "maybe"
    }
  }
}
