# An ephemeral variable in count. Before the marked-value guard in
# buildExpansion, gocty.FromCtyValue panicked on the marked number and took
# the whole run down; internal/command/e2etest/testdata/ephemeral-repetition/
# count is upstream's version of the same configuration.

variable "size" {
  type      = number
  default   = 2
  ephemeral = true
}

resource "aws_s3_bucket" "data" {
  count = var.size

  bucket = "estate-data"
}
