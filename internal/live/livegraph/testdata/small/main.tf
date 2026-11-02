# The live-graph golden fixture (#2285): blocks, a count pair, a module
# called with count, and two reads of other estates.

resource "aws_s3_bucket" "logs" {
  bucket = "web-logs"
}

locals {
  bucket_id = aws_s3_bucket.logs.id
}

resource "aws_sqs_queue" "foo" {
  count = 2
  name  = "foo-${count.index}"
}

resource "aws_sqs_queue" "bar" {
  count = 2
  name  = "bar-${count.index}"
  redrive_policy = jsonencode({
    deadLetterTargetArn = aws_sqs_queue.foo[count.index].arn
  })
}

module "app" {
  source      = "./modules/app"
  count       = 2
  logs_bucket = local.bucket_id
}

data "terraform_estate_outputs" "net" {
  estate = "network"
}

data "aws_vpc" "shared" {
  filter {
    name   = "tag:tofu-estate"
    values = ["core"]
  }
}

resource "aws_subnet" "app" {
  vpc_id     = data.aws_vpc.shared.id
  cidr_block = data.terraform_estate_outputs.net.outputs.cidr
}
