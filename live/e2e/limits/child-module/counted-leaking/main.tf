# The count.index-leaking call's target. Its own contents are inside the
# stateless subset; the only thing the fixture proves is that the call
# itself - "counted-leaking" in ../main.tf - is refused, because that call's
# own arguments index into a collection at count.index
# (suffix = var.suffixes[count.index]), not because count on a module block
# is banned outright. See "counted" for the admitted case, where count is
# set but nothing in the call reads count.index at all.

variable "suffix" {
  type = string
}

resource "aws_vpc" "main" {
  cidr_block = "10.47.0.0/16"

  tags = {
    Name = var.suffix
  }
}
