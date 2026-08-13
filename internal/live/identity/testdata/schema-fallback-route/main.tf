# aws_route's own shape, stood up with fake schemas so the fallback can be
# asked about it directly rather than through Resolve, which would find the
# hand row in DefaultTable before synthesis ever ran. route_table_id is the
# only required identity attribute, and destination_cidr_block is one of
# three optional-for-import alternatives - the shape #39 is about, where a
# single required attribute is not the whole identity.

resource "aws_route" "r" {
  route_table_id         = "rtb-0123456789abcdef0"
  destination_cidr_block = "10.0.0.0/16"
}
