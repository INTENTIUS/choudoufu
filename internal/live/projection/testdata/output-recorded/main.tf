# GitHub issue #2073: constant root outputs, so evaluation can always answer
# and only the estate's records decide the plan's before side.

# The store holds no record for this one: a newly added output.
output "never_recorded" {
  value = 14
}

# Recorded as 14.
output "recorded_same" {
  value = 14
}

# Recorded as 7, configured as 30.
output "recorded_diff" {
  value = 30
}

# Sensitive outputs are never recorded, so this one is evaluated.
output "secret" {
  value     = "s3cr3t"
  sensitive = true
}
