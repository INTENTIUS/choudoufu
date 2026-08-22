variable "name" {
  type = string
}

resource "aws_ecs_cluster" "this" {
  count = 1

  name = var.name
}

output "arn" {
  value = try(aws_ecs_cluster.this[0].arn, null)
}
