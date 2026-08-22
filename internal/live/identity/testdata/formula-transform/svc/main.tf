variable "name" {
  type = string
}

variable "cluster_arn" {
  type = string
}

locals {
  cluster_name = try(element(split("/", var.cluster_arn), 1), "")
}

resource "aws_ecs_service" "this" {
  count = 1

  name    = var.name
  cluster = var.cluster_arn
}

resource "aws_appautoscaling_target" "this" {
  count = 1

  resource_id        = "service/${local.cluster_name}/${aws_ecs_service.this[0].name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}

resource "aws_appautoscaling_policy" "this" {
  for_each = toset(["cpu"])

  name               = each.key
  resource_id        = aws_appautoscaling_target.this[0].resource_id
  scalable_dimension = aws_appautoscaling_target.this[0].scalable_dimension
  service_namespace  = aws_appautoscaling_target.this[0].service_namespace
}
