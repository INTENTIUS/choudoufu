resource "aws_eip" "pool" {
  count = 2

  tags = {
    Name = "pool"
  }
}
