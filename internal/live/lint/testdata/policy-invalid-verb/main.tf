# Fixture for RulePolicyVerb: "delete" is not a valid verb for the
# declared+tagged quadrant (see internal/live/policy.ValidVerbs).

terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged = "delete"
    }
  }
}
