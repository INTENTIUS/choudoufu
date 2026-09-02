# Clean-pass fixture: the default written out by hand. GitHub issue #365
# ruling 4's no_source_create toggle - an instance with no record, no
# marker and no derivable identity is refused rather than planned as a
# create.
#
# It must mean exactly the same thing as omitting it - #101's standing
# lesson, where writing a documented default by hand was a lint error while
# omitting it was clean.

terraform {
  live {
    estate = "my-estate"
    strict {
      no_source_create = "refuse"
    }
  }
}
