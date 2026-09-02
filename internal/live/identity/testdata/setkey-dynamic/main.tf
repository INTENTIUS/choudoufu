provider "aws" {
  region = "us-east-1"
}

resource "aws_iam_group" "admins" {
  name = "admins"
}

module "child" {
  source = "./child"

  s = ["alpha", aws_iam_group.admins.name]
}
