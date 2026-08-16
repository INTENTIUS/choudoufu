# The same idiom made ineligible: the data source reads a managed
# resource, so the site gets the class-specific not-readable wording
# instead of the generic dynamic-value text, and the estate stays
# language-blocked.
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
