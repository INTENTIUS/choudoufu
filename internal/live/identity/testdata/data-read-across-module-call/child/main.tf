variable "azs" {
  type = list(string)
}

resource "aws_cloudwatch_log_group" "per_az" {
  for_each = toset(var.azs)

  name = "/net/${each.value}"
}

resource "aws_cloudwatch_log_group" "by_index" {
  count = length(var.azs)

  name = "/net-idx/${var.azs[count.index]}"
}
