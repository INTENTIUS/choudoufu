# The govuk-aws shape, reduced: the projected argument is itself a data
# source read, so the projection is only answerable after that source has
# been read. The dependency edge has to survive classification for the read
# order to make that true.
data "test_zone" "a" {
  name = "example.com."
}

resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = data.test_zone.a.zone_id
}

data "test_subnet" "of_instance" {
  id = aws_instance.web.subnet_id
}

resource "aws_cloudwatch_log_group" "per_subnet" {
  name = "/subnets/${data.test_subnet.of_instance.cidr_block}"
}
