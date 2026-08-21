# The first coalescelist() argument provably expands to zero instances, so
# coalescelist() would move on to its second argument - but that argument
# is a plain variable reference, neither a splat over a managed resource
# nor a literal list. Whether IT is empty (and so whether coalescelist()
# would skip it too) is not knowable from configuration alone, so this
# package must refuse with its own specific reason rather than guess.

variable "extra_route_table_ids" {
  type    = list(string)
  default = ["rtb-extra"]
}

resource "aws_route_table" "database" {
  count = 0
}

resource "aws_route_table_association" "unrecognized" {
  subnet_id = "subnet-fake"
  route_table_id = element(
    coalescelist(aws_route_table.database[*].id, var.extra_route_table_ids),
    0,
  )
}
