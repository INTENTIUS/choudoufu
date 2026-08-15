# length(<resource>) where the resource has neither count nor for_each: it
# is a single object, and OpenTofu evaluates length() of an object over its
# attributes rather than "how many instances there are" - a number this
# resolver has no schema to reproduce. The rule must not guess 1 just
# because the resource happens to be declared once; it has to keep
# refusing this honestly, the same as before the fix.
resource "aws_eip" "single" {
}

resource "aws_cloudwatch_log_group" "sized_by_single" {
  count = length(aws_eip.single)

  name = "/estate/log"
}
