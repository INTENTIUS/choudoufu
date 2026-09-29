# Fixture for GitHub issue #1480: the lookalike guard on a plain plan.
#
# One needs-discovery aws_security_group instance. aws_security_group's list
# schema carries the filter block, so a plain plan (CollectUnclaimed unset)
# lists it server-side estate-filtered - and a live security group whose
# tofu-estate/tofu-address tags were stripped out of band never crosses the
# wire, which is exactly the resource the guard was written to warn about.
#
# The name argument is here because internal/live/foreign's matchTable
# matches an aws_security_group on it: the unmarked live group in the fake
# cloud carries the same name, so once discovery lets it through the guard
# has a confirmed content match to name rather than a cardinality guess.

resource "aws_security_group" "main" {
  name        = "estate-main"
  description = "the estate's own security group"
}
