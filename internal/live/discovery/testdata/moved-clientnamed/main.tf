# Fixture for GitHub issue #244 half 2 crossed with #198: a moved block over a
# CLIENT-NAMED type, whose identity comes out of the configuration.
#
# testdata/moved-rename is the same shape over aws_subnet, a needs-discovery
# type, and it exercises a different branch of discovery's scan loop: a
# needs-discovery address is bound through declared.entryFor and the
# declares() short-circuit is never reached. Only a client-named type reaches
# it, and #244's identity check now sits inside it.
#
# So this fixture is what proves the check does not break a pending move. The
# live bucket is still carrying "aws_s3_bucket.old" - which is exactly what a
# moved block means before the marker rewrite lands - while its identity is
# the one the configuration computes for aws_s3_bucket.renamed. Marker and
# identity disagree; the move is why, and the check has to say so.

resource "aws_s3_bucket" "renamed" {
  bucket = "tofu-stateless-e2e-renamed"
}

moved {
  from = aws_s3_bucket.old
  to   = aws_s3_bucket.renamed
}
