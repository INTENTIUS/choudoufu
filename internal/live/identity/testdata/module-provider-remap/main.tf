terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

provider "aws" {
  region = "eu-west-1"
}

provider "aws" {
  alias  = "east"
  region = "us-east-1"
}

resource "random_pet" "seed" {
  length = 2
}

# Two calls of one module, remapped to two aliased provider configurations
# in two regions, each naming its log group after the same record-backed
# parent. They are two live objects, not one.
module "west" {
  source = "./child"
  name   = "svc-${random_pet.seed.id}"
}

module "east" {
  source = "./child"
  providers = {
    aws = aws.east
  }
  name = "svc-${random_pet.seed.id}"
}
