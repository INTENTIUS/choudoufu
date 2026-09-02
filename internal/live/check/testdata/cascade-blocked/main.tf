# The false-"you are fine" guard: the group's identity fails for a reason
# that has nothing to do with a data read (no value for the required "name"
# argument at all), and the group policy still depends on it. The cascade
# onto the policy must stay a hard "Unresolvable identity" refusal -
# propagation only ever fires when the traced-back failure is itself a
# data-read-eligible one.
#
# The pair used to be aws_cloudwatch_log_group / aws_cloudwatch_log_stream.
# #190 taught the table the provider's auto-generated-name convention, and
# a log group is one of the 37 types that convention covers: omitting its
# name now defers to discovery instead of refusing, so the fixture stopped
# demonstrating anything. aws_iam_group's name carries no such promise in
# the provider's own Argument Reference, so it still fails on its own.
resource "aws_iam_group" "orphan" {
}

resource "aws_iam_group_policy" "attached" {
  group  = aws_iam_group.orphan.name
  name   = "policy"
  policy = "{}"
}
