# A marker_repair setting outside the vocabulary altogether: the typo case.
# Refused by RuleStrictMarkerRepair with the "not a marker_repair setting"
# wording, which lists all three and says which the build implements.

terraform {
  live {
    estate = "my-estate"
    strict {
      marker_repair = "sometimes"
    }
  }
}
