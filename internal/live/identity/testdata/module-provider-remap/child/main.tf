variable "name" {
  type = string
}

# The identity reads var.name, which is what sends the resolver up into the
# parent module to evaluate the module call's own argument.
resource "aws_cloudwatch_log_group" "this" {
  name = var.name
}
