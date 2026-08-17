terraform {
  live {
    estate = "projection-identity-cycle"
  }
}

# Each bucket names itself after the other, so the identity of either can only
# be built from the identity of the other. This is the configuration shape
# projection's "Cyclic parent-derived identities" refusal describes - and
# identity resolution refuses it first, which is what
# TestAnalyzeCannotReachCyclicParentDerivedIdentities is about.
resource "aws_s3_bucket" "a" {
  bucket = aws_s3_bucket.b.bucket
}

resource "aws_s3_bucket" "b" {
  bucket = aws_s3_bucket.a.bucket
}
