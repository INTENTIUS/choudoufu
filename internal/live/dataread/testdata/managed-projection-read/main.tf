# Issue #193's read side: data.test_subnet's own argument reads an attribute
# aws_instance.web's block SETS, so the value is in the configuration and the
# provider must be asked for that exact subnet rather than for an unknown.
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = "subnet-0abc"
}

data "test_subnet" "of_instance" {
  id = aws_instance.web.subnet_id
}

resource "aws_cloudwatch_log_group" "per_subnet" {
  name = "/subnets/${data.test_subnet.of_instance.cidr_block}"
}
