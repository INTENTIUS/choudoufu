resource "aws_vpc" "grandchild" {
  cidr_block = "10.2.0.0/16"

  tags = {
    tofu-estate = "estate-grandchild"
  }
}
