# A configuration that asks for both live mode and a state backend. The
# decoder refuses it: the two disagree about where the truth lives.
terraform {
  live {
    estate = "live-unit"
  }

  backend "local" {
    path = "somewhere.tfstate"
  }
}

resource "aws_s3_bucket" "data" {
  bucket = "tofu-live-unit-data"
}
