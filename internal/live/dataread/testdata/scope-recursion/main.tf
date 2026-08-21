# GitHub issue #352's -target scope, in the shape that defeated the check
# while it lived over the demand ROOTS: data.test_zone.a is reached only as
# data.test_record.b's dependency, so classify's recursion stored it and no
# check over the roots ever saw it.
data "test_zone" "a" {
  name = "example.com."
}

data "test_record" "b" {
  zone = data.test_zone.a.zone_id
}

resource "test_thing" "keep" {
  name = "keep"
}

output "record_id" {
  value = data.test_record.b.id
}
