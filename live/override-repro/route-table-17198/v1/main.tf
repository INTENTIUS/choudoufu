resource "aws_vpc" "v" { cidr_block = "10.233.0.0/16" }
resource "aws_internet_gateway" "g" { vpc_id = aws_vpc.v.id }
resource "aws_vpc" "peer" { cidr_block = "10.234.0.0/16" }
resource "aws_vpc_peering_connection" "p" {
  vpc_id      = aws_vpc.v.id
  peer_vpc_id = aws_vpc.peer.id
  auto_accept = true
}
resource "aws_route_table" "rt" {
  vpc_id = aws_vpc.v.id
  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.g.id
  }
  route {
    cidr_block                = "10.234.0.0/24"
    vpc_peering_connection_id = aws_vpc_peering_connection.p.id
  }
}
