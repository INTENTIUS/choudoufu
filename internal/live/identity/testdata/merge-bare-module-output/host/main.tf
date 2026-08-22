variable "quantity" {
  type = number
}

variable "base_configuration" {
  type = any
}

# Every component of this name is written in the caller's own files: "demo"
# and "eu-west-1a" come out of module.network's output expression, the index
# out of count. Nothing here reads the poisoned leaf.
resource "aws_iam_role" "host" {
  count = var.quantity

  name               = "${var.base_configuration["name_prefix"]}-${var.base_configuration["zone"]}-${count.index}"
  assume_role_policy = "{}"
}

# The control: this one's name reads the leaf that genuinely is a live
# subnet ID, and must render NOTHING however tolerant the merge becomes - no
# value may be invented for it and no sibling's value may stand in for it.
#
# Since internal/live/identity/computedselect.go it does resolve, to the
# formula `role-${aws_subnet.public.id}`, which is what the control is
# actually about rather than an exception to it: a formula names the exact
# parent instance and attribute and renders off the LIVE object, so its
# import ID is empty until that object is read, and the assertion in
# moduleargspelling_test.go - id must be "" - still holds. A concrete
# resolution here would be the failure.
resource "aws_iam_role" "poisoned" {
  count = var.quantity

  name               = "role-${var.base_configuration["subnet"]}"
  assume_role_policy = "{}"
}
