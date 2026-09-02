terraform {
  live {
    estate = "projection-identity-cycle"
  }
}

# Each group names itself after the other, so the identity of either can only
# be built from the identity of the other. This is the configuration shape
# projection's "Cyclic parent-derived identities" refusal describes - and
# identity resolution refuses it first, which is what
# TestAnalyzeCannotReachCyclicParentDerivedIdentities is about.
#
# The type is aws_iam_group, not aws_s3_bucket as this fixture read before
# GitHub issue #289: aws_s3_bucket is taggable and enumerable, so
# [resolver.markerFallback] now answers the SELF side of a genuine cycle
# with the marker the same way it answers any other unresolvable identity
# on a [DiscoverableFallbackTypes] member - b resolves NEEDS_DISCOVERY
# through its own marker, which breaks the cycle rather than leaving it
# unreachable, and a then resolves PARENT_DERIVED off b's now-completed
# resolution. That is a different, correct outcome from what THIS fixture
# pins - that identity resolution's own walk order keeps a genuine cycle
# from ever reaching [projection.CyclicIdentityDiagnostics] with two
# mutually-dependent Formulas - so the type here stays outside the gate.
resource "aws_iam_group" "a" {
  name = aws_iam_group.b.name
}

resource "aws_iam_group" "b" {
  name = aws_iam_group.a.name
}
