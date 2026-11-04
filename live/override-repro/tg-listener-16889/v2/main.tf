resource "aws_lb" "l" {
  name               = "${var.sfx}-alb"
  internal           = true
  load_balancer_type = "application"
  subnets            = [aws_subnet.a.id, aws_subnet.b.id]
}
resource "aws_lb_target_group" "t" {
  name     = "${var.sfx}-tg"
  port     = 8080
  protocol = "HTTP"
  vpc_id   = aws_vpc.v.id
}
resource "aws_lb_listener" "x" {
  load_balancer_arn = aws_lb.l.arn
  port              = 80
  protocol          = "HTTP"
  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.t.arn
  }
}
