locals {
  # The marker's estate value (live/MARKERS.md, P0.3), distinct from the
  # demo estate's "stateless-e2e" and the lambda cohort's "lambda-cohort"
  # so all three never collide if ever applied against the same account
  # side by side.
  estate_tag = "messaging-cohort"
}
