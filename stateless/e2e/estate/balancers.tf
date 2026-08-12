# Coverage: marker path over a chain of ELBv2 objects (aws_lb,
# aws_lb_target_group, aws_lb_listener — each one named in configuration and
# identified by an ARN ELBv2 mints), plus a parent-derived attachment whose
# identity is the target group's live ARN joined with its target and port.
# Second slice of the survey's marker cohort (#20) and #21's parent-derived
# slice.
#
# The load balancer spans the estate's two subnets, which is what an
# application load balancer requires and also what makes this block a real
# consumer of a for_each'd marker-path parent.
#
# security_groups is deliberately not set. An internal load balancer works
# without one against the emulator, and every argument this fixture writes
# that the API does not serve back becomes a permanent in-place diff under
# markers (stateless/LIMITATIONS.md, "Config-only attributes"). The same rule
# is why the attachment below sets no availability_zone: ELBv2 accepts it,
# DescribeTargetHealth never returns it, and setting it makes every plan
# propose replacing the attachment forever — which is exactly what the probe
# for this slice observed before the argument came out.

resource "aws_lb" "main" {
  name               = "tofu-stateless-e2e-lb"
  internal           = true
  load_balancer_type = "application"
  subnets            = [for s in aws_subnet.this : s.id]

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_lb.main"
  }
}

resource "aws_lb_target_group" "app" {
  name        = "tofu-stateless-e2e-tg"
  port        = 80
  protocol    = "HTTP"
  vpc_id      = aws_vpc.main.id
  target_type = "ip"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_lb_target_group.app"
  }
}

resource "aws_lb_listener" "app" {
  load_balancer_arn = aws_lb.main.arn
  port              = 80
  protocol          = "HTTP"

  default_action {
    type             = "forward"
    target_group_arn = aws_lb_target_group.app.arn
  }

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_lb_listener.app"
  }
}

# Parent-derived: identity is the composite (target group ARN, target, port),
# joined with commas the way the provider's import ID is. The target is a
# plain address inside the first subnet's CIDR — an IP target keeps this row
# about the attachment rather than about EC2 instances, which floci cannot
# bring to a running state (lex00/floci#32). Untaggable by type, like the
# other parent-derived rows.
resource "aws_lb_target_group_attachment" "app" {
  target_group_arn = aws_lb_target_group.app.arn
  target_id        = "10.42.1.55"
  port             = 80
}
