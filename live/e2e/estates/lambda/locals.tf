locals {
  # The marker's estate value (live/MARKERS.md, P0.3), distinct from the
  # demo estate's "stateless-e2e" so the two cohorts never collide if ever
  # applied against the same account side by side.
  estate_tag = "lambda-cohort"
}
