resource "aws_vpc" "v" { cidr_block = "10.232.0.0/16" }
resource "aws_security_group" "sg" {
  name   = "${var.sfx}-sg"
  vpc_id = aws_vpc.v.id
}
resource "aws_security_group_rule" "r" {
  type              = "ingress"
  from_port         = 443
  to_port           = 443
  protocol          = "tcp"
  cidr_blocks       = ["10.0.0.0/24", "10.0.1.0/24"]
  security_group_id = aws_security_group.sg.id
}
