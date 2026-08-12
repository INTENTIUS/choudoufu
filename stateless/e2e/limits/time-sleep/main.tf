# Limits fixture: RuleLogicalResource, time_sleep.
#
# A time_ resource's entire value is "did this already happen," which is a
# question only a stored record can answer. See stateless/LIMITATIONS.md.

resource "time_sleep" "wait" {
  create_duration = "30s"
}
