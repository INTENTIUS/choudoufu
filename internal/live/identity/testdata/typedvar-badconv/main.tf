provider "aws" {
  region = "us-east-1"
}

resource "aws_sqs_queue" "seed" {
  name = "seed"
}

module "child" {
  source = "./child"

  # "not-a-number" cannot be converted to the child's declared map(number).
  # OpenTofu rejects this configuration outright; nothing here may turn it
  # into a marker.
  s = {
    a = "not-a-number"
    b = aws_sqs_queue.seed.max_message_size
  }
}
