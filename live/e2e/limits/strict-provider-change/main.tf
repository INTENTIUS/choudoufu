# Limits fixture: RuleStrictProviderChange (GitHub issue #906; maintainer
# ruling, 2026-09-06).
#
# `provider_change = "move"` is neither of the two settings this fork's
# schema defines. The refusal is the point, for the same reason
# strict-no-source-create's is: the two settings are opposites ("refuse",
# the default, blocks and names a resource block that has moved to a
# different provider configuration while a live object in the one it left
# still carries this estate's marker for its address; "recreate" selects
# stock's own behavior of planning the create and leaving that object
# where it is), so a spelling that is neither is a question this package
# cannot answer without guessing which way the author meant to move. And
# "move" is the plausible typo, because moving is precisely what this
# toggle cannot do - no cloud API relocates an object between regions.
# See live/LIMITATIONS.md, "strict-provider-change".

terraform {
  live {
    estate = "my-estate"
    strict {
      provider_change = "move"
    }
  }
}
