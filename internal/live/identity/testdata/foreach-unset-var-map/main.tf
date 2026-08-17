# The half of the unknown question that must keep refusing: a required root
# variable of a collection type, with no default and no value. The whole
# collection is unknown, so nothing can say WHICH instances exist - which is
# also why stock OpenTofu refuses this run before it plans anything ("No value
# for required variable"). Refusing is parity; see live/corpus-manifest.json's
# #183 ruling on the govuk-aws cohort.
variable "buckets" {
  type = map(string)
}

resource "aws_s3_bucket" "this" {
  for_each = var.buckets

  bucket = each.value
}
