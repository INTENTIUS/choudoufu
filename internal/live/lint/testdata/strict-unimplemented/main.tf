# A marker_repair setting the schema defines and this build does not
# implement. "report" is the second of the two; live/e2e/limits/
# strict-marker-repair carries "never". Both are refused by
# RuleStrictMarkerRepair with the not-yet-implemented wording.

terraform {
  live {
    estate = "my-estate"
    strict {
      marker_repair = "report"
    }
  }
}
