# Issue #240, sites 11 and 12: lint.staticForEachKeys and
# stamp.staticForEachKeys both test the for_each value for marks and then
# read its ELEMENTS. cty hoists a marked element's mark to the containing
# SET, but not out of a LIST or TUPLE, so `for_each = [var.secret]` arrives
# as an unmarked tuple holding a marked string and AsString panicked.
#
# There is no diagnostic to assert: both functions report "cannot check" for
# a marked for_each, and identity resolution refuses the block on its own
# grounds. Not panicking is the whole assertion.
variable "secret" {
  type      = string
  default   = "s"
  sensitive = true
}

resource "aws_ecr_repository" "r" {
  for_each = [var.secret]
  name     = each.key

  tags = {
    Name = "x"
  }
}
