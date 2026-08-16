provider "aws" {
  region = "us-east-1"
}

resource "aws_iam_group" "admins" {
  name = "admins"
}

module "child" {
  source = "./child"

  # "a" leaves the optional attribute out entirely, so the value the child
  # sees carries the type default rather than a null. "b" reaches a managed
  # resource, which is what forces the per-key fallback.
  users = {
    a = { name = "alice" }
    b = { name = aws_iam_group.admins.name }
  }
}
