# The bucket name is built from uuid() and the log group's from timestamp().
# Both evaluate cleanly to a real string, and a different one on every run:
# the resolution that used to come out of here was CONCRETE, with a
# fabricated import ID, so every plan proposed a create and every apply
# leaked another bucket. Resolution must refuse, by name.
resource "aws_s3_bucket" "data" {
  bucket = "estate-${uuid()}"
}

resource "aws_cloudwatch_log_group" "app" {
  name = "/estate/${timestamp()}"
}
