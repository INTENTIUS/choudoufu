resource "aws_vpc" "v" {
  cidr_block = "10.231.0.0/16"
}
data "aws_availability_zones" "az" {}
resource "aws_subnet" "a" {
  vpc_id            = aws_vpc.v.id
  cidr_block        = "10.231.1.0/24"
  availability_zone = data.aws_availability_zones.az.names[0]
}
resource "aws_subnet" "b" {
  vpc_id            = aws_vpc.v.id
  cidr_block        = "10.231.2.0/24"
  availability_zone = data.aws_availability_zones.az.names[1]
}
