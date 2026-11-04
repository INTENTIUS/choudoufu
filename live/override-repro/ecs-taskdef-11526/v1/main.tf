resource "aws_ecs_task_definition" "t" {
  family                   = "${var.sfx}-td"
  requires_compatibilities = ["FARGATE"]
  network_mode             = "awsvpc"
  cpu                      = "256"
  memory                   = "512"
  volume { name = "logs" }
  container_definitions = jsonencode([{
    name        = "nginx"
    image       = "public.ecr.aws/nginx/nginx:stable"
    essential   = true
    portMappings = [{ containerPort = 443, hostPort = 443, protocol = "tcp" }]
    mountPoints = [{ readOnly = false, containerPath = "/var/log/app", sourceVolume = "logs" }]
    volumesFrom = []
  }])
}
