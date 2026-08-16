# The false-"you are fine" guard: the log group's identity fails for a
# reason that has nothing to do with a data read (no value for the required
# "name" argument at all), and the log stream still depends on it. The
# cascade onto the log stream must stay a hard "Unresolvable identity"
# refusal - propagation only ever fires when the traced-back failure is
# itself a data-read-eligible one.
resource "aws_cloudwatch_log_group" "orphan" {
}

resource "aws_cloudwatch_log_stream" "per_zone" {
  log_group_name = aws_cloudwatch_log_group.orphan.name
  name            = "stream"
}
