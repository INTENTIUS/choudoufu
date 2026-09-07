# NO live block, deliberately: an implied record store would put both
# resources below on the record rung and take the distinction this fixture
# exists for with it.
#
# Two instances of ONE untaggable, server-assigned type, so that the only
# difference between them is the cause identity resolution assigns.
#
# aws_cloudfront_cache_policy is GitHub issue #272's admitted case: the
# provider's argument reference and the CloudFormation registry schema both
# call the name this configuration supplies unique within the account and
# region, so the identity table carries a UniqueName row for it. An instance
# that STATES that name resolves with cause UNIQUE_NAME - AWS refuses to
# issue it twice, so a later run finds the object by name whether or not
# anything ever marked it. An instance that omits it cannot: the provider
# mints the whole identity at create time and nothing in the configuration
# says what the object will be called.
#
# So the first must NOT be refused for having nowhere to write a marker and
# the second must be, from one run over one file, with one schema serving
# both. See TestStampGate_UniqueNameCauseIsExemptFromTheUnmarkedApplyRefusal.

resource "aws_cloudfront_cache_policy" "exempt" {
  name = "exempt-cache-policy"
}

resource "aws_cloudfront_cache_policy" "refused" {
}
