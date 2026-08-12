# A configuration that names everything it can: the input to the admission
# upgrade (derive_signal.go). Every aws_s3_bucket and aws_sqs_queue instance
# sets the argument the provider requires for import, and the aws_vpc sets
# nothing that names it, which is the whole of what the schemas could not
# decide for themselves.

resource "aws_s3_bucket" "one" {
  bucket = "tofu-signal-one"
}

resource "aws_s3_bucket" "two" {
  for_each = toset(["a", "b"])
  bucket   = "tofu-signal-${each.key}"
}

# Outside the hand table: the shape a wiring batch would otherwise pick up
# by hand.
resource "aws_sqs_queue" "jobs" {
  name = "tofu-signal-jobs"
}

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}
