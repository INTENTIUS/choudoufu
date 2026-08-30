variable "prefix" {
  type = string
}

variable "pod_size" {
  type = number
}

resource "aws_iam_role" "pod_role" {
  count = var.pod_size

  name               = "${var.prefix}-team-${format("%04d", count.index)}-role"
  assume_role_policy = "{}"
}
