locals {
  # The marker's estate value (live/MARKERS.md, P0.3), distinct from the
  # demo estate's "stateless-e2e" and from every other cohort's own tag, so
  # no two cohorts collide if ever applied against the same account side by
  # side.
  estate_tag = "remainder-cohort"
}
