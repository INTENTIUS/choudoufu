# A sensitive variable in count. The mark reaches buildExpansion the same
# way an ephemeral one does, so the same guard covers both; this fixture
# exists so a fix that special-cased marks.Ephemeral would still fail.

variable "size" {
  type      = number
  default   = 3
  sensitive = true
}

resource "aws_s3_bucket" "data" {
  count = var.size

  bucket = "estate-data"
}
