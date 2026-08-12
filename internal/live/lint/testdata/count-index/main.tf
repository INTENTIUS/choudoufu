# Fixture: every position count.index must be caught in (P3.4). All types
# here are in the v0 admission table and none of the other rules apply, so
# only RuleCountIndex fires, once per resource.

# A plain, non-tag argument built from count.index.
resource "aws_vpc" "plain_arg" {
  count = 2

  cidr_block = "10.${count.index}.0.0/16"
}

# The canonical case: count.index interpolated into a tag value.
resource "aws_vpc" "in_tag" {
  count = 2

  cidr_block = "10.50.0.0/16"

  tags = {
    Name = "vpc-${count.index}"
  }
}

# count.index inside a nested block the resource type defines (not a
# meta-argument block): aws_security_group's ingress block.
resource "aws_security_group" "nested_block" {
  count = 2

  name   = "sg-nested"
  vpc_id = aws_vpc.plain_arg[0].id

  ingress {
    from_port   = count.index
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["10.0.0.0/8"]
  }
}

# count.index reached only through a conditional expression: a traversal
# walk catches this because it inspects the expression's referenced
# variables, not because the literal text "count.index" appears next to an
# identity-bearing property.
resource "aws_vpc" "conditional" {
  count = 2

  cidr_block = count.index == 0 ? "10.60.0.0/16" : "10.61.0.0/16"
}
