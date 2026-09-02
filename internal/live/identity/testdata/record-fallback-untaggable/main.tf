# aws_autoscaling_group's own shape: a ratified table row (identity is the
# "name" argument, resolved statically whenever a configuration states it),
# but the type has no top-level tags map at all - its `tag` argument is a
# nested block, not the map internal/live/markers.TagSurface requires - so
# an instance whose name is left to the name_prefix convention has neither a
# static name nor anywhere to carry an ownership marker. With a record_store
# declared, [RecordFallbackType] is what tells this instance apart from an
# ordinary refusal: see [TestRecordFallbackClassifiesUntaggableNamePrefix].
terraform {
  live {
    estate = "record-fallback-untaggable"

    record_store "local" {
      path = ".tofu-records"
    }
  }
}

resource "aws_autoscaling_group" "prefixed" {
  name_prefix         = "web-"
  max_size            = 1
  min_size            = 0
  vpc_zone_identifier = ["subnet-example"]
}

resource "aws_autoscaling_group" "named" {
  name                = "web-static"
  max_size            = 1
  min_size            = 0
  vpc_zone_identifier = ["subnet-example"]
}
