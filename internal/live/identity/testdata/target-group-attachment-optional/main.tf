# Component.OmitIfAbsent's availability_zone and quic_server_id segments
# (#286). The provider (aws 6.59.0) documents the comma-joined form as
# built from "target_group_arn, target_id, and optionally port and
# availability_zone separated by commas", with a literal example that stops
# at port:
#
#	arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123,i-0123456789abcdef0,8080
#
# and separately documents quic_server_id as an Optional Identity Schema
# attribute with its own literal example value ("0x1a2b3c4d5e6f7a8b", the
# "Target using QUIC" section) but never composes a single comma string
# that carries port, availability_zone AND quic_server_id together - no
# such combined example exists on the page. base below reproduces the
# documented 3-field string exactly. with_az and with_quic each add one
# documented-but-not-jointly-exampled optional segment, following the same
# comma rule the page states for availability_zone and the same value the
# page gives for quic_server_id.
resource "aws_lb_target_group_attachment" "base" {
  target_group_arn = "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123"
  target_id         = "i-0123456789abcdef0"
  port              = 8080
}

resource "aws_lb_target_group_attachment" "with_az" {
  target_group_arn  = "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123"
  target_id         = "i-0123456789abcdef0"
  port              = 8080
  availability_zone = "us-west-2a"
}

resource "aws_lb_target_group_attachment" "with_quic" {
  target_group_arn = "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123"
  target_id        = "i-0123456789abcdef0"
  port              = 8080
  quic_server_id    = "0x1a2b3c4d5e6f7a8b"
}

resource "aws_alb_target_group_attachment" "base" {
  target_group_arn = "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/my-tg/abc123"
  target_id         = "i-0123456789abcdef0"
  port              = 8080
}
