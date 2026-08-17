terraform {
  live {
    estate = "stamp-untaggable-record-located"

    # The onboarded form. GitHub issue #270: the marker answers "may I
    # delete this" and the identity answers "which object is this", and
    # declaring a record_store is what supplies the second for a type that
    # can never carry the first.
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

# aws_eip_association is the type this whole mechanism was opened for - it is
# the sole markerless blocker on two corpus estates. It is deliberately NOT a
# type any other admission route reaches: a fixture whose type gets admitted
# some other way stops exercising the located path, and this file was already
# rewritten once when its previous type was admitted by unique-name binding.
resource "aws_eip_association" "this" {
  allocation_id = "eipalloc-example"
  instance_id   = "i-example"
}
