variable "s" {
  type = map(number)
}

resource "aws_sqs_queue" "q" {
  for_each = var.s
  name     = "q-${each.value}"
}
