# A managed block whose own instance keys are not knowable before the plan
# has no aggregate shape, so nothing about it is projectable - including
# subnet_id, which would have evaluated perfectly well on its own.
data "test_zone" "z" {
  name = "example.com."
}

resource "aws_instance" "fleet" {
  count         = data.test_zone.z.zone_id == "x" ? 1 : 2
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = "subnet-static"
}

data "test_subnet" "unexpandable" {
  id = aws_instance.fleet[0].subnet_id
}

resource "aws_cloudwatch_log_group" "per_subnet" {
  name = "/subnets/${data.test_subnet.unexpandable.cidr_block}"
}
