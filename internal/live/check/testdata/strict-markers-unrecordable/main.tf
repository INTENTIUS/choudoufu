# GitHub issue #365, slice 2: the selection an operator may not have.
#
# aws_cognito_user_pool_client's Import section documents a composite string
# and nothing corroborates that its exported `id` is the whole of it, so it is
# in identity.IDNotProvenWholeTypes and LocatedIdentityPlanFor refuses it. A
# record written from `id` alone would hold a fragment, and a fragment handed
# back to a later import is a WRONG identity rather than a missing one -
# invisible to every verdict-level check until that import fails, with the
# object already live.
#
# So the selection is refused, and it is refused rather than quietly not
# applied: an operator who believes a tag has been freed will spend it
# somewhere else. See live/LIMITATIONS.md, "strict-markers-unrecordable".
#
# The type is in the types list and declared nowhere. That is what keeps the
# fixture to the one refusal being tested - a declared resource of this type
# would raise its own, unrelated, verdicts - and it is also the shape the rule
# is meant to catch first: the selection is a standing instruction, and saying
# no when it is written beats saying no the first time a resource of that type
# appears.

terraform {
  live {
    estate = "markers-unrecordable"

    record_store "local" {}

    strict {
      markers "record" {
        types = ["aws_cognito_user_pool_client"]
      }
    }
  }
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
