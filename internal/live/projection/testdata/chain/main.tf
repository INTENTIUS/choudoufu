# Three resource blocks for the ordering test. The chain the test cares
# about (subnet -> route table -> association, each derived from the last)
# lives in the hand-written resolutions, not here: this configuration only
# has to supply three real resource blocks with a provider, since that is
# all the projection builder reads out of it.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "aws_subnet" "a" {
  vpc_id     = "vpc-fixed"
  cidr_block = "10.0.1.0/24"
}

resource "aws_route_table" "main" {
  vpc_id = "vpc-fixed"
}

resource "aws_route_table_association" "a" {
  subnet_id      = aws_subnet.a.id
  route_table_id = aws_route_table.main.id
}
