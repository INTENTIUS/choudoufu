# A demanded data source naming a managed resource in depends_on: rule 4's
# other half. The arguments are static; the ordering constraint is what
# defers the read past the plan.
resource "aws_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t3.micro"
}

data "aws_route53_zone" "gated" {
  name       = "example.com."
  depends_on = [aws_instance.web]
}

resource "aws_cloudwatch_log_group" "per_zone" {
  name = "/zones/${data.aws_route53_zone.gated.zone_id}"
}
