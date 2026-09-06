# Clean-pass fixture: the default written out by hand. GitHub issue #906's
# provider_change toggle - a resource block that has moved to a different
# provider configuration, while a live object in the one it left still
# carries this estate's marker for its address, is refused rather than
# recreated over the top.
#
# It must mean exactly the same thing as omitting it - #101's standing
# lesson, where writing a documented default by hand was a lint error while
# omitting it was clean.

terraform {
  live {
    estate = "my-estate"
    strict {
      provider_change = "refuse"
    }
  }
}
