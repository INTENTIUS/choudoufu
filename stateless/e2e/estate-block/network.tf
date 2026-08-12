# Coverage: marker path (aws_vpc, aws_subnet, aws_security_group) — the same
# admission row the main estate's network.tf covers. No for_each here (the
# main estate's aws_subnet.this carries that row already); one subnet is
# enough for this fixture's purpose, which is proving plain plan/apply, not
# re-proving admission coverage.
#
# No tags argument on any resource below, unlike the main estate: this is the
# point of estate-block (README.md) — every marker here is one stamping
# writes at apply time, never one hand-written in config.

resource "aws_vpc" "main" {
  cidr_block = "10.99.0.0/16"
}

resource "aws_subnet" "app" {
  vpc_id     = aws_vpc.main.id
  cidr_block = "10.99.1.0/24"
}

resource "aws_security_group" "main" {
  name        = "stateless-e2e-block-main"
  description = "estate-block fixture security group"
  vpc_id      = aws_vpc.main.id
}
