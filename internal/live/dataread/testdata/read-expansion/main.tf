# count on a data source, driven by another data source's value: rule 2
# allows it because the dependency is read first, and the instances share
# one read since the arguments cannot reference count.index in this stage.
data "test_zone" "a" {
  name = "example.com."
}

data "test_record" "b" {
  count = length(data.test_zone.a.zone_id) > 0 ? 2 : 0
  zone  = data.test_zone.a.zone_id
}

resource "aws_cloudwatch_log_group" "per_record" {
  count = 2
  name  = "/records/${count.index}/${data.test_record.b[count.index].id}"
}
