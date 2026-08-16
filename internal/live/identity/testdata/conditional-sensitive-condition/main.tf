# Audit of #196's resolveConditional: cty.Value.True panics on a marked
# value ("value is marked, so must be unmarked first"), and a bool variable
# declared sensitive evaluates to exactly that. Before the fix this brought
# the whole run down from check.Dir - the entry point both front ends call -
# rather than refusing the site.
#
# lint's and stamp's own static evaluators both test IsMarked before
# reading a value; identity's conditional path did not.

variable "use_primary" {
  type      = bool
  default   = true
  sensitive = true
}

resource "aws_subnet" "this" {
  cidr_block = "10.0.0.0/24"
}

resource "aws_route_table" "primary" {}
resource "aws_route_table" "secondary" {}

resource "aws_route_table_association" "assoc" {
  subnet_id      = aws_subnet.this.id
  route_table_id = var.use_primary ? aws_route_table.primary.id : aws_route_table.secondary.id
}
