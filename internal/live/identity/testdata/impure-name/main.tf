# One name is built from uuid() and the other from timestamp(). Both
# evaluate cleanly to a real string, and a different one on every run: the
# resolution that used to come out of here was CONCRETE, with a fabricated
# import ID, so every plan proposed a create and every apply leaked another
# object. Resolution must refuse, by name.
#
# Both blocks are aws_iam_group, not aws_s3_bucket/aws_cloudwatch_log_group
# as this fixture read before GitHub issue #289: both of those types are
# taggable and enumerable, so their own marker fallback would now answer
# these refusals too - safely, since a discovered instance never renders an
# import ID from the impure call - but that is a different, correct
# behaviour from what THIS fixture pins: that an impure call never reaches
# a resolved identity in the first place, for the general, ungated case.
# aws_iam_group carries no tags argument and stays outside that gate.
resource "aws_iam_group" "data" {
  name = "estate-${uuid()}"
}

resource "aws_iam_group" "app" {
  name = "estate-${timestamp()}"
}
