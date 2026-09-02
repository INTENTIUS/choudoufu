# Issue #193 shape A, reduced: a demanded data source whose argument reads a
# managed resource attribute that the resource block itself SETS. The value
# is in the configuration, so nothing has to be read from the cloud or from
# state to know it - which is what Options.ProjectManagedArguments answers.
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
  subnet_id     = var.subnet_id
}

variable "subnet_id" {
  type    = string
  default = "subnet-0abc"
}

data "aws_subnet" "of_instance" {
  id = aws_instance.web.subnet_id
}

# The counterpart the projection must NOT answer: private_dns is assigned by
# the provider, so the block does not set it and there is nothing in the
# configuration to project.
data "aws_route53_zone" "of_instance" {
  name = aws_instance.web.private_dns
}

resource "aws_cloudwatch_log_group" "per_subnet" {
  name = "/subnets/${data.aws_subnet.of_instance.cidr_block}"
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.of_instance.zone_id}"
}
