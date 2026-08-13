# The provider's identity schema for aws_named_thing marks its identity
# attribute Optional+Computed, and this configuration declines to set it.
# That is the aws_vpc answer: the cloud names this object, so the schemas plus
# this configuration admit nothing, and marker discovery is what finds it.

resource "aws_named_thing" "unnamed" {
  size = 3
}
