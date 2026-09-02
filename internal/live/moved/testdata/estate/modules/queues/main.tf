resource "aws_sqs_queue" "doi" {
  name = "doi"
}

resource "aws_sqs_queue" "stray" {
  name = "stray"
}
