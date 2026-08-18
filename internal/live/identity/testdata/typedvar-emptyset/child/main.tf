variable "s" {
  type = set(string)
}

resource "aws_sqs_queue" "q" {
  for_each = { for k, v in var.s : "n-${k}" => v }
  name     = "q-${each.value}"
}
