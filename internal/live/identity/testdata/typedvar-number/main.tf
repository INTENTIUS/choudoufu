provider "aws" {
  region = "us-east-1"
}

resource "aws_sqs_queue" "seed" {
  name = "seed"
}

module "child" {
  source = "./child"

  # One literal, one dynamic - so whole-object evaluation fails and the
  # per-key fallback runs. The literal is a STRING where the child declares
  # map(number), which is where OpenTofu's declared-type conversion and this
  # resolver's raw reading part company.
  s = {
    a = "007"
    b = aws_sqs_queue.seed.max_message_size
  }
}
