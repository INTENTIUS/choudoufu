variable "name" {
  type = string
}

variable "override" {
  type    = string
  default = "shared"
}

# var.name is declared and passed but never reaches the name argument, so
# coalesce takes the default and both call instances resolve to the one
# identity "shared".
resource "aws_iam_user" "wrapped" {
  name = coalesce(var.override, "u-${var.name}")
}
