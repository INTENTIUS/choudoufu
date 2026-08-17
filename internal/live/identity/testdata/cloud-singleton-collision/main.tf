provider "aws" {
  region = "eu-west-1"
}

variable "where" {
  type    = string
  default = "eu-west-1"
}

# Two blocks for the one setting the region actually has: one inheriting the
# provider block's region, one naming the same region through a variable.
resource "aws_vpc_block_public_access_options" "inherited" {
  internet_gateway_block_mode = "block-bidirectional"
}

resource "aws_vpc_block_public_access_options" "spelled" {
  region                      = var.where
  internet_gateway_block_mode = "off"
}
