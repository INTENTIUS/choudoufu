variable "groups" { type = set(string) }

resource "aws_lb_target_group" "this" {
  for_each = var.groups

  name     = each.key
  port     = 80
  protocol = "HTTP"
}

# The shape this fixture is about: an output that publishes the WHOLE
# resource rather than one attribute of it, so the caller indexes and selects
# on the far side of the module boundary. terraform-aws-modules/alb's own
# `output "target_groups" { value = aws_lb_target_group.this }` is this line
# verbatim.
output "target_groups" { value = aws_lb_target_group.this }
