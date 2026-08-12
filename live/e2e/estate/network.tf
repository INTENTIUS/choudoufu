# Coverage: marker path (aws_vpc, aws_subnet, aws_security_group), for_each
# stable keys (aws_subnet.this), parent-derived (aws_route,
# aws_route_table_association). aws_route_table and aws_internet_gateway are
# not coverage rows themselves — they exist only as the parent/target the
# parent-derived row needs. See README.md.

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_vpc.main"
  }
}

resource "aws_subnet" "this" {
  for_each = local.subnets

  vpc_id            = aws_vpc.main.id
  cidr_block        = each.value.cidr
  availability_zone = each.value.az

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_subnet.this:${each.key}"
  }
}

resource "aws_security_group" "main" {
  name        = "stateless-e2e-main"
  description = "estate fixture security group"
  vpc_id      = aws_vpc.main.id

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_security_group.main"
  }
}

# Supporting infrastructure for the parent-derived row below: a route needs a
# route table to live in and a target to point at.

resource "aws_route_table" "main" {
  vpc_id = aws_vpc.main.id

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_route_table.main"
  }
}

resource "aws_internet_gateway" "main" {
  vpc_id = aws_vpc.main.id

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_internet_gateway.main"
  }
}

# Parent-derived: identity is the composite (route table, destination CIDR).
# aws_route carries no tags argument in the AWS API — untaggable by type, not
# an oversight.
resource "aws_route" "internet_gateway" {
  route_table_id         = aws_route_table.main.id
  destination_cidr_block = "0.0.0.0/0"
  gateway_id             = aws_internet_gateway.main.id
}

# Parent-derived: identity is the composite (subnet, route table). Also
# untaggable by type.
resource "aws_route_table_association" "this" {
  for_each = aws_subnet.this

  subnet_id      = each.value.id
  route_table_id = aws_route_table.main.id
}

# Coverage: marker path, one resource per rule
# (aws_vpc_security_group_ingress_rule / _egress_rule — EC2 mints an sgr- ID
# per rule, and the per-rule resource types exist precisely so each rule has
# its own identity and its own tags, unlike aws_security_group's inline
# blocks). Third slice of the survey's marker cohort (#20).

resource "aws_vpc_security_group_ingress_rule" "https" {
  security_group_id = aws_security_group.main.id
  cidr_ipv4         = "10.42.0.0/16"
  from_port         = 443
  to_port           = 443
  ip_protocol       = "tcp"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_vpc_security_group_ingress_rule.https"
  }
}

resource "aws_vpc_security_group_egress_rule" "all" {
  security_group_id = aws_security_group.main.id
  cidr_ipv4         = "0.0.0.0/0"
  ip_protocol       = "-1"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_vpc_security_group_egress_rule.all"
  }
}

# Coverage: marker path (aws_nat_gateway — EC2 mints the nat- ID; nothing in
# the block names it). Same slice (#20). connectivity_type is "private" so
# the gateway needs no EIP allocation — a public NAT gateway would couple
# this row to the fungible EIP pool for no coverage gain.
resource "aws_nat_gateway" "main" {
  subnet_id         = aws_subnet.this["a"].id
  connectivity_type = "private"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_nat_gateway.main"
  }
}
