# Clean-pass fixture for GitHub issue #101: a policy block that spells out
# all four of internal/live/policy.DefaultVerb's verbs explicitly, with no
# scope block.
#
# internal/live/policy/verb.go's DefaultVerb doc states the invariant this
# pins: "a configuration with no policy block and one whose policy block
# spells out these same four verbs produce the identical [Policy] value."
# checkLivePolicy used to break it. Its scope rule fired on any quadrant
# written as "delete", and undeclared_tagged's default IS "delete", so
# writing the default out by hand was a hard lint error while omitting it
# was clean.
#
# Only undeclared_untagged's delete is account reconciliation and only it
# needs a scope: undeclared_tagged's delete is the ordinary orphan sweep
# over resources already carrying this estate's ownership marker, which is
# its own scope. See internal/live/discovery/policy.go's DefaultVerb
# no-op branch and statelessPolicyReconcile's gate on UndeclaredUntagged.

terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged     = "converge"
      declared_untagged   = "refuse"
      undeclared_tagged   = "delete"
      undeclared_untagged = "keep"
    }
  }
}
