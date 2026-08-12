# A route table whose identity has already been recovered (P2's discovery
# does this for real; the test writes the resolution by hand) and a route
# whose identity is a formula over that route table's live ID. This is the
# smallest configuration in which a parent-derived instance has a parent
# the projection can actually materialize, which is the case the ordering
# and formula-rendering tests need and which the estate fixture cannot
# produce until marker discovery lands.

terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

resource "aws_route_table" "main" {
  vpc_id = "vpc-fixed"
}

resource "aws_route" "internet_gateway" {
  route_table_id         = aws_route_table.main.id
  destination_cidr_block = "0.0.0.0/0"
}
