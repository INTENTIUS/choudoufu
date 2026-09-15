# Fixture: the same two shapes as testdata/count-index-pure-scalar and
# testdata/count-index-nonlinear, at a count ABOVE countIndexDomainMax's old
# value of 256.
#
# The point is the bound, not the shapes. Both resources here were decided
# identically before the bound moved - refused - because an oversized count
# made countIndexDomain report itself unavailable, and the syntactic rule
# that then decides refuses every function call in a rendered value, format
# included. One of them deserved that answer and the other did not.
#
# terralith-gen's own estate is what found this: it declares
# count = 2 * SCALE (issue #574), so every size from scale 129 up carried a
# count over 256 and choudoufu refused to plan an estate it plans perfectly
# well at scale 128. See issue #1076.

# Distinct at every one of its 300 indices: format is injective on
# 0..299 and nothing else varies. Must be ADMITTED.
resource "aws_route53_record" "wide_distinct" {
  count = 300

  zone_id = "Z0123456789ABCDEFGHI"
  name    = "wide-${format("%04d", count.index)}.example.com"
  type    = "A"
  ttl     = 300
  records = ["10.0.0.1"]
}

# Collides: index 0 and index 3 render the same value, and so do 297 other
# pairs. Must be REFUSED, and refused because the domain SAW the collision
# rather than because it declined to look.
resource "aws_route53_record" "wide_collides" {
  count = 300

  zone_id = "Z0123456789ABCDEFGHI"
  name    = "wide-${count.index % 3}.example.com"
  type    = "A"
  ttl     = 300
  records = ["10.0.0.1"]
}
