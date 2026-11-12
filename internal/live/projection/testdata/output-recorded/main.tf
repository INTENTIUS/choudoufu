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

# A resource-shaped output: the apply recorded kms_key_id as "", the legacy
# SDK's answer for an unset string, and the projection evaluates it as a
# null string. Same value to the provider, so the plan is a no-op.
output "recorded_legacy_empty" {
  value = { id = "lg-1", kms_key_id = tostring(null) }
}

# Recorded with name = "", configured as "named": a real change.
output "recorded_empty_changed" {
  value = { id = "lg-2", name = "named" }
}

# corpus-iam-policy's day2_replace: a JSON document attribute. The replace's
# apply recorded the provider's normalized (compact) text, and the
# projection evaluates the configuration's own layout, which the provider's
# read keeps because the two are JSON-equivalent. Same document: no-op.
output "recorded_json_whitespace" {
  value = <<EOT
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Action": [
        "ec2:Describe*"
      ],
      "Effect": "Allow",
      "Resource": "*"
    }
  ]
}
EOT
}

# Recorded with a different Action: a real change in the document.
output "recorded_json_changed" {
  value = <<EOT
{
  "Version": "2012-10-17",
  "Statement": [{"Action": ["ec2:Describe*"], "Effect": "Allow", "Resource": "*"}]
}
EOT
}

# Recorded as "1.0", configured as "1": not a JSON document, so text decides.
output "recorded_json_scalar" {
  value = "1"
}
