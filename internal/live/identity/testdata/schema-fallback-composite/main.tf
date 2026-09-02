# A type whose identity the provider requires two attributes for. Both are
# required arguments, so the schemas say the configuration names the object -
# and they do not say what character joins the two halves into an import ID.
# The fallback refuses it; a table entry is where that inference belongs.

resource "aws_composite_thing" "pair" {
  left  = "l"
  right = "r"
}
