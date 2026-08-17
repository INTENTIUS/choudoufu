terraform {
  live {
    estate = "stamp-untaggable-with-schema"

    # No record_store, and its ABSENCE is load-bearing since GitHub issue
    # #270. This fixture is the "not onboarded" side of that issue: a type
    # with nowhere to write a marker and nothing to record which object it
    # is, which is still refused and must stay refused. The onboarded side
    # - the same type under a record_store, admitted as RECORD_LOCATED -
    # is stamp-untaggable-record-located next door.
    #
    # This block did declare a record_store until #270 landed, and it did
    # nothing: before that issue the store had no bearing on a markerless
    # type at all. Adding one back would silently move this fixture to the
    # other side of the split and take TestStampGate_GenuinelyUntaggableTypeStillRefuses
    # with it.
  }
}

resource "aws_cloudfront_cache_policy" "example" {
  name    = "example-policy"
  min_ttl = 1
}
