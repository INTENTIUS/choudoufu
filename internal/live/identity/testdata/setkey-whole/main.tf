provider "aws" {
  region = "us-east-1"
}

module "child" {
  source = "./child"

  s = ["e-alpha", "e-beta"]
}
