resource "aws_ecs_cluster" "c" { name = "${var.sfx}-cl" }
resource "aws_ecs_task_definition" "t" {
  family                   = "${var.sfx}-aas"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"
  container_definitions    = jsonencode([{ name = "n", image = "public.ecr.aws/nginx/nginx:stable", essential = true }])
}
resource "aws_ecs_service" "s" {
  name            = "${var.sfx}-svc"
  cluster         = aws_ecs_cluster.c.id
  task_definition = aws_ecs_task_definition.t.arn
  desired_count   = 0
  launch_type     = "FARGATE"
  network_configuration { subnets = [aws_subnet.a.id, aws_subnet.b.id] }
}
resource "aws_appautoscaling_target" "t" {
  min_capacity       = 0
  max_capacity       = 1
  resource_id        = "service/${aws_ecs_cluster.c.name}/${aws_ecs_service.s.name}"
  scalable_dimension = "ecs:service:DesiredCount"
  service_namespace  = "ecs"
}
