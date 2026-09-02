# GitHub issue #178's try() slice. terraform-aws-modules writes
#
#   role   = try(aws_iam_role.codedeploy[0].id, "")
#   vpc_id = try(aws_vpc_ipv4_cidr_block_association.this[0].vpc_id,
#                aws_vpc.this[0].id, "")
#
# where each indexed resource's count is `<bool> ? 1 : 0`. Which argument
# the language selects is decided by whether the earlier ones raise an
# error, and for an index into a resource this package has already expanded,
# that is settled by the expansion. See fallback.go.
#
# Every argument AFTER the one that must be selected names an UNDECLARED
# resource, so a resolved formula also proves the later arguments were never
# consulted.

variable "create_primary" {
  type    = bool
  default = true
}

variable "create_secondary" {
  type    = bool
  default = false
}

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_subnet" "other" {
  cidr_block = "10.0.1.0/24"
}

resource "aws_subnet" "third" {
  cidr_block = "10.0.2.0/24"
}

# count = 1: instance [0] exists, so indexing it cannot raise an error.
resource "aws_route_table" "primary" {
  count = var.create_primary ? 1 : 0
}

# count = 0: no instance [0] at all, so indexing it raises "Invalid index"
# on every run and try() moves past it.
resource "aws_route_table" "secondary" {
  count = var.create_secondary ? 1 : 0
}

# The first argument's instance exists, so it is the one selected - even
# though its value is not known until apply, which is exactly the case
# hcl's tryfunc returns a dynamic value for rather than falling through.
resource "aws_route_table_association" "first_arg_lives" {
  subnet_id      = aws_subnet.this.id
  route_table_id = try(aws_route_table.primary[0].id, aws_route_table.ghost_second.id)
}

# The first argument's instance does not exist, so the second is what the
# language evaluates.
resource "aws_route_table_association" "falls_through" {
  subnet_id      = aws_subnet.other.id
  route_table_id = try(aws_route_table.secondary[0].id, aws_route_table.primary[0].id, aws_route_table.ghost_third.id)
}

# A resource with no count at all is one instance under the no-key, which a
# bare reference addresses; nothing about the rule needs an index.
resource "aws_route_table" "solo" {}

resource "aws_route_table_association" "bare_reference" {
  subnet_id      = aws_subnet.third.id
  route_table_id = try(aws_route_table.solo.id, aws_route_table.ghost_fourth.id)
}
