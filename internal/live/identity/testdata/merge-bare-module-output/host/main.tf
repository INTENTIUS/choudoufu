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
# subnet ID, and must stay unresolved however tolerant the merge becomes.
resource "aws_iam_role" "poisoned" {
  count = var.quantity

  name               = "role-${var.base_configuration["subnet"]}"
  assume_role_policy = "{}"
}
