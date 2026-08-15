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

data "terraform_remote_state" "network" {
  backend = "local"
}

moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.new
}
