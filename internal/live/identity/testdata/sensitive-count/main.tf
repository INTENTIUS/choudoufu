# A sensitive variable in count. Stock unmarks a sensitive count, because
# instance keys 0..n-1 disclose nothing about the value, and refuses only an
# ephemeral one (internal/lang/evalchecks/eval_count.go). #1792 brought the
# identity path into line, so this resolves three instances; ephemeral-count
# is the fixture that must keep refusing.

variable "size" {
  type      = number
  default   = 3
  sensitive = true
}

resource "aws_s3_bucket" "data" {
  count = var.size

  bucket = "estate-data-${count.index}"
}
