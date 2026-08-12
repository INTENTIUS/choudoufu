# The called module. Its own contents are inside the stateless subset, so the
# only thing the fixture proves is that the call itself is refused.

resource "aws_vpc" "main" {
  cidr_block = "10.43.0.0/16"
}
