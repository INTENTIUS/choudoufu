provider "aws" {
  region = "us-east-1"
}

resource "aws_iam_group" "admins" {
  name = "admins"
}

module "child" {
  source = "./child"

  # "b" is left out entirely, so the child's optional attribute default is the
  # only thing that supplies it. "c" reaches a managed resource, which is what
  # forces the per-key fallback.
  s = {
    a = "alpha"
    c = aws_iam_group.admins.name
  }
}
