terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
  live {
    estate = "root-estate"
  }
}

module "vendored" {
  source = "./mod"
}
