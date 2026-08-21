resource "aws_vpc" "kept" {
  cidr_block = "10.44.0.0/16"

  tags = {
    Name = "kept"
  }
}
