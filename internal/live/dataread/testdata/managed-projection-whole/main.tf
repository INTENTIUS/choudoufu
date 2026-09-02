# The projection is partial by construction - it carries what the body sets
# and nothing the provider assigns - so a reference that never names an
# attribute must refuse rather than get a truncated object back.
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = "subnet-0abc"
}

data "test_subnet" "whole" {
  id = jsonencode(aws_instance.web)
}

resource "aws_cloudwatch_log_group" "per_subnet" {
  name = "/subnets/${data.test_subnet.whole.cidr_block}"
}
