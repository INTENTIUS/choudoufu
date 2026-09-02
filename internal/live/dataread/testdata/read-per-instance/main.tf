# A for_each-expanded data source whose own argument reads its own
# each.value: legitimate per-block repetition (rule 1/2), not a dynamic
# value. Read must read each instance separately, with that instance's own
# each.value bound, rather than sharing one answer across instances whose
# arguments genuinely differ (#193).
data "test_zone" "z" {
  for_each = toset(["a.example.com.", "b.example.com."])
  name     = each.value
}

resource "aws_cloudwatch_log_group" "per_zone" {
  for_each = data.test_zone.z
  name     = "/zones/${each.value.zone_id}"
}
