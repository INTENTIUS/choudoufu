variable "name" {
  type = string
}

variable "label" {
  type = string
}

variable "owner" {
  type = string
}

resource "aws_iam_user" "this" {
  name = "${var.name}-${var.label}-${coalesce(var.owner, "unset")}"
}
