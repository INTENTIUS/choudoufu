variable "prefix" {
  type = string
}

variable "slots" {
  type    = list(string)
  default = ["blue", "green", "amber", "violet"]
}

resource "aws_iam_role" "pod_role" {
  count = length(var.slots)

  name               = "${var.prefix}-${var.slots[count.index]}-role"
  assume_role_policy = "{}"
}
