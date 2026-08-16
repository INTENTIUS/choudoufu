# Fixture with several rules firing at once, to pin the reported order: by
# source position within a file, whatever order the configuration's maps
# happen to iterate in.

terraform {
  backend "local" {
    path = "terraform.tfstate"
  }
}

resource "aws_accessanalyzer_analyzer" "web" {
  name = "example-rule"

  provisioner "local-exec" {
    command = "echo hello"
  }
}

resource "null_resource" "trigger" {
}

resource "aws_s3_bucket" "old" {
  bucket = "tofu-stateless-lint-old"
}

# Refused because the address it moves from is still declared above; a moved
# block markers can follow is reported by nobody (GitHub issue #198).
moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}
