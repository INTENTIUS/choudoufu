# Fixture for TestScanTypeContentMatch (contentmatch_e2e_test.go): a real
# aws_cloudfront_realtime_log_config declaration, which identity.ContentMatchTypes
# already carries a real binding for (Argument "name", CFN type
# AWS::CloudFront::RealtimeLogConfig, PropertyPath Name) - so this exercises
# the actual generated table rather than a synthetic one.
#
# Untaggable with no native list resource, so it reaches scanType's
# content-match branch cleanly. aws_cloudfront_cache_policy, this fixture's
# type until issue #272's merge, also carries a UniqueName row from the
# separate, independently-evolved unique-name mechanism (reading the same
# two-source evidence); scanType now defers to that stronger, admission-backed
# leg whenever both apply, so a cache-policy fixture would no longer reach
# scanTypeContentMatch through the real dispatch this test exists to exercise.
# aws_route53_key_signing_key, tried next, turned out to be a false positive
# the same merge's import-grammar.json correction removed: its own doc
# bullet promises uniqueness only "in the same hosted zone," narrower than
# the account-and-region scope a listing covers, so uniquename.Asserted
# correctly stopped qualifying it.
#
# This fixture declares only the one argument content match reads (`name`);
# Discover never validates a declaration against the provider's own schema,
# so the type's other required arguments are not needed for this test.

resource "aws_cloudfront_realtime_log_config" "x" {
  name = "my-policy"
}
