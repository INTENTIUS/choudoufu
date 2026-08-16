# A demanded data source whose provider block needs more than static
# evaluation: rule 3, the exact line ConfiguredProvider draws.
provider "aws" {
  region = aws_instance.web.availability_zone
}

resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
}

data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.primary.zone_id}"
}
