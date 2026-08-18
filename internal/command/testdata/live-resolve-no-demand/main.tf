# A configuration that refuses for a reason no managed read could settle: the
# group's name calls uuid(), which returns a different value every run.
# statelessResolve must never configure a provider for this one - a second
# pass would be handed the identical inputs and produce the identical answer,
# at the cost of starting a plugin and making a plan call per resource.
#
# The block is aws_iam_group, not aws_s3_bucket as this fixture read before
# GitHub issue #289: aws_s3_bucket is taggable and enumerable, so its own
# marker fallback would now answer "Identity derived from an impure
# function" for it too - correctly, and still with no provider configured,
# since the fallback fires inside identity resolution itself, before this
# file's second pass ever runs - but a resolution that RESOLVES is not the
# shape this fixture is for; it exists to prove the provider seam is never
# reached for a refusal, and a marker-answered instance is not a refusal
# to test that against. aws_iam_group has no tags argument and stays
# outside that gate, so it keeps refusing exactly as before.
resource "aws_iam_group" "b" {
  name = "prefix-${uuid()}"
}
