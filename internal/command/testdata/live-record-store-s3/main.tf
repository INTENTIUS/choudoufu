terraform {
  backend "s3" {
    bucket = "acme-tfstate"
    key    = "prod/network/terraform.tfstate"
    region = "us-east-1"
  }
}

resource "terraform_data" "marker" {}
