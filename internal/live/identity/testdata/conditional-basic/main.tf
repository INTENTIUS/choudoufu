# GitHub issue #196: `var.use_primary ? aws_route_table.primary.id :
# aws_route_table.secondary.id` is the corpus shape - a conditional
# expression choosing between two resources' own identity attributes. Both
# associations below pick a real route table on one branch and name an
# UNDECLARED one on the other, so if [resolver.resolveConditional] ever
# evaluated the branch it did not select, resolution would fail with
# "Reference to undeclared resource" instead of succeeding.

variable "use_primary" {
  type    = bool
  default = true
}

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table" "primary" {}
resource "aws_route_table" "secondary" {}

# Condition is a bare var reference and selects the true branch.
resource "aws_route_table_association" "true_branch" {
  subnet_id      = aws_subnet.this.id
  route_table_id = var.use_primary ? aws_route_table.primary.id : aws_route_table.ghost_false.id
}

# Condition is a unary-negated var reference and selects the false branch -
# also proving evalStatic's ordinary expression handling, not just a bare
# traversal, decides which way the conditional goes.
resource "aws_route_table_association" "false_branch" {
  subnet_id      = aws_subnet.this.id
  route_table_id = !var.use_primary ? aws_route_table.ghost_true.id : aws_route_table.secondary.id
}
