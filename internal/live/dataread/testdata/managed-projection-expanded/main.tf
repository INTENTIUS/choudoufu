# A count-expanded managed block: the projection has to carry one object per
# instance, with that instance's own count.index bound, or an indexed
# reference reads instance 0 three times and a splat answers one element for
# a three-instance block.
resource "aws_instance" "fleet" {
  count         = 3
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = "subnet-${count.index}"
}

data "test_subnet" "indexed" {
  count = 3
  id    = aws_instance.fleet[count.index].subnet_id
}

resource "aws_cloudwatch_log_group" "per_subnet" {
  count = 3
  name  = "/subnets/${data.test_subnet.indexed[count.index].cidr_block}"
}
