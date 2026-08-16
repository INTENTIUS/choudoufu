provider "aws" {
  region = "us-east-1"
}

resource "aws_sqs_queue" "seed" {
  name = "seed"
}

module "child" {
  source = "./child"

  s = {
    a = "007"
    b = aws_sqs_queue.seed.max_message_size
  }
}
