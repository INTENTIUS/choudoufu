# Clean-pass fixture: the default written out by hand. GitHub issue #365's
# secrets toggle, HANDOFF.md's "secrets the configuration generates are
# stored there the way stock stores them".
#
# It must mean exactly the same thing as omitting it - #101's standing
# lesson, where writing a documented default by hand was a lint error while
# omitting it was clean.

terraform {
  live {
    estate = "my-estate"
    strict {
      secrets = "store"
    }
  }
}
