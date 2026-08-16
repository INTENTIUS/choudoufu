# The count-expanded call's target. Its own contents are inside the
# stateless subset, and since issue #195 the call itself - "counted" in
# ../main.tf - is admitted too: its count (a literal 1) is statically
# evaluable and none of the call's own arguments read count.index, so
# RuleChildModule no longer reports it, and the five walkers traverse into
# aws_vpc.main below at "module.counted[0].aws_vpc.main". See
# "counted-leaking" for the still-refused case, where count is equally
# static but the call's own arguments do read count.index.

resource "aws_vpc" "main" {
  cidr_block = "10.44.0.0/16"
}
