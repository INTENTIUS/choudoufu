# The #183 cohort's shape carried across a module boundary, which is where the
# partial-argument rebuild (internal/live/identity/partialargs.go) could have
# reached it and must not.
#
# `buckets` is a required root variable with no value. Under this package's
# loader that arrives as an UNKNOWN rather than an error, so the module
# argument below evaluates successfully and the rebuild's fallback never runs
# at all - which is the point: a rule that treated "unknown" as "evaluable"
# would name instances here out of nothing. Nothing in this configuration says
# which instances exist, and stock OpenTofu refuses the run outright ("No value
# for required variable"), so refusing is parity. See
# live/corpus-manifest.json's #183 ruling on the govuk-aws cohort.
variable "buckets" {
  type = map(string)
}

module "b" {
  source = "./mod"

  buckets = var.buckets
}
