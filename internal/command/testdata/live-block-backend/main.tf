# A configuration that asks for both stateless mode and a state backend. The
# decoder refuses it: the two disagree about where the truth lives.
terraform {
  live {
    estate = "stateless-unit"
  }

  backend "local" {
    path = "somewhere.tfstate"
  }
}

resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-unit-data"
}
