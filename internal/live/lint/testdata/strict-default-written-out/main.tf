# Clean-pass fixture: the default written out by hand. It must mean exactly
# the same thing as omitting it - #101's standing lesson, where writing a
# documented default by hand was a lint error while omitting it was clean.

terraform {
  live {
    estate = "my-estate"
    strict {
      marker_repair = "repair"
    }
  }
}
