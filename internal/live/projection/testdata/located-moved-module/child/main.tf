resource "aws_eip_association" "bastion" {
  allocation_id = "eipalloc-declared"
  instance_id   = "i-declared"
}
