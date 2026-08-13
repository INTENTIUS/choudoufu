provider "aws" {
  alias  = "east"
  region = "us-east-1"
}

resource "aws_cloudwatch_event_rule" "web" {
  provider = aws.east
  name     = "example-rule"
}
