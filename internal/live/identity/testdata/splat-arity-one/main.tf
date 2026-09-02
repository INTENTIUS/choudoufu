# GitHub issue #196's second half: terraform-aws-modules/eks writes
#
#   cluster_security_group_id = var.cluster_create_security_group
#     ? join("", aws_security_group.cluster.*.id)
#     : var.cluster_security_group_id
#
# in local.tf, where aws_security_group.cluster is count = <bool> ? 1 : 0.
# The outer conditional resolves since #196's first half; the join over the
# splat is what this fixture is for. The three shapes below are the three
# things the arity rule claims, one each.

variable "create_primary" {
  type    = bool
  default = true
}

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

# A second subnet so that from_one names a different pair from from_join;
# they select the same route table on purpose, and two blocks resolving to
# one identity is itself a refusal.
resource "aws_subnet" "other" {
  cidr_block = "10.0.1.0/24"
}

# count = 1: the splat is a one-element list, and its one element is
# instance [0] - not the resource as a whole.
resource "aws_route_table" "primary" {
  count = var.create_primary ? 1 : 0
}

# No count and no for_each at all: a splat over an unrepeated resource
# wraps it into a one-element list, which is arity one just the same.
resource "aws_route_table" "solo" {}

# The eks shape, verbatim in structure: a conditional whose selected branch
# is a join("", <splat>) and whose unselected branch names an UNDECLARED
# route table, so a resolved formula also proves the other branch was never
# consulted.
resource "aws_route_table_association" "from_join" {
  subnet_id      = aws_subnet.this.id
  route_table_id = var.create_primary ? join("", aws_route_table.primary.*.id) : aws_route_table.ghost.id
}

# The separator is not empty. At arity one there is no "between" for it to
# appear in, so this must resolve exactly as join("") does - the rule is
# about how many elements there are, not about which separator was written.
resource "aws_route_table_association" "nonempty_separator" {
  subnet_id      = aws_subnet.this.id
  route_table_id = join("-", aws_route_table.solo[*].id)
}

# one() is the same arity claim spelled shorter, and the modern [*] splat
# is the same node the legacy .*. spelling parses to.
resource "aws_route_table_association" "from_one" {
  subnet_id      = aws_subnet.other.id
  route_table_id = one(aws_route_table.primary[*].id)
}
