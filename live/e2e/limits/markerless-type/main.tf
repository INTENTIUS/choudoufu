# Limits fixture: RuleMarkerlessType.
#
# aws_emr_instance_group is in internal/live/identity.MarkerlessTypes, the
# roster tools/row-gen derives from live/survey-full.json rather than
# maintains: EMR mints the instance group's own id at create time, and the
# type carries no tags argument, so the ownership marker that is the only
# handle left has nowhere to be written. The survey's own row reads "no
# identity schema in v6.59.0; untaggable, no native list resource and no
# Cloud Control list handler, so no admission path recovers it".
#
# This fixture is deliberately not a credential type, even though two of
# them (aws_iam_access_key, aws_iot_certificate) are on the roster and do
# fire this rule. Their governing exclusion is the credential ruling, and a
# fixture that used one would leave a reader unable to tell which of the two
# reasons this rule is actually expressing.
#
# The example moves the day a provider release adds a tags argument to this
# type, or the day row-gen's roster stops naming it. Either way
# TestLimitsEnforced fails loudly rather than passing on a fixture that no
# longer exercises the rule.

resource "aws_emr_instance_group" "task" {
  cluster_id    = "j-EXAMPLECLUSTER"
  instance_type = "m5.xlarge"
  instance_count = 2
}
