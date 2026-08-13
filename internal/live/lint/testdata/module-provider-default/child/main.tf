provider "aws" {
  region = "us-west-2"
}

resource "aws_cloudwatch_event_rule" "web" {
  name = "example-rule"
}
