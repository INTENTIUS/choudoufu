variable "estate" {
  type    = string
  default = "estate-root"
}

resource "aws_vpc" "root" {
  cidr_block = "10.0.0.0/16"

  tags = {
    tofu-estate = var.estate
  }
}

module "child" {
  source = "./child"
}
