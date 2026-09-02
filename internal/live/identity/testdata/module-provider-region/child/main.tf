provider "aws" {
  region = "eu-west-1"
}

resource "aws_arczonalshift_autoshift_observer_notification_status" "inchild" {}

resource "aws_sqs_queue" "inchild" {
  name = "jobs"
}
