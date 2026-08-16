# A demanded data source whose argument reads a managed resource: rule 4.
# In stock OpenTofu this read defers until the instance is planned, so
# there is nothing honest to read before the plan.
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
}

data "aws_route53_zone" "of_instance" {
  name = aws_instance.web.private_dns
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.of_instance.zone_id}"
}
