# A nested block is not an attribute, and the cty shape it takes is the
# provider schema's NestingMode rather than anything the body says. It is not
# projected, so a reference to one refuses.
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"

  root_block_device {
    volume_size = 20
  }
}

data "test_subnet" "nested" {
  id = tostring(aws_instance.web.root_block_device.volume_size)
}

resource "aws_cloudwatch_log_group" "per_subnet" {
  name = "/subnets/${data.test_subnet.nested.cidr_block}"
}
