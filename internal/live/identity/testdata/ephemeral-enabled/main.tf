# An ephemeral variable in lifecycle.enabled. Before the marked-value guard
# in buildExpansion, cty's Value.False asserted the value was unmarked and
# panicked; internal/command/e2etest/testdata/ephemeral-repetition/enabled is
# upstream's version of the same configuration.

variable "on" {
  type      = bool
  default   = true
  ephemeral = true
}

resource "aws_s3_bucket" "data" {
  bucket = "estate-data"

  lifecycle {
    enabled = var.on
  }
}
