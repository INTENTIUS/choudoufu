# Issue #1144's end-to-end fixture: one taggable, marked object of a type
# the Resource Groups Tagging API indexes in us-east-1 and nowhere else.
#
# aws_iam_instance_profile is the type, and it is the only sensible choice.
# It is what issue #881's silently-omitted destroy is about; it carries a
# tags argument the provider honours (live/floci-capabilities.json's
# tagging-sweep row for this digest confirms the emulator echoes both
# markers back through list-instance-profile-tags); it has NO native list
# resource at provider 6.59.0, so the per-type leg can only reach it through
# Cloud Control; and Cloud Control's AWS::IAM::InstanceProfile listing
# returns InstanceProfileName/Arn/Path and no tags at all, in every region,
# which is what makes the two legs distinguishable by their OUTCOME and not
# only by their name.
#
# No role is attached. An instance profile with no role is a complete,
# tagged object as far as every leg under test is concerned, and leaving the
# role out keeps aws_iam_role - the one IAM type real AWS indexes in NO
# region - out of a fixture that is about the two it does index.

resource "aws_iam_instance_profile" "demo" {
  name = "perregion-e2e-demo"

  tags = {
    tofu-estate  = "perregion-e2e"
    tofu-address = "aws_iam_instance_profile.demo"
  }
}

# The second type, and it is here because the two fail differently.
#
# aws_iam_policy DOES have a native list resource (iam:ListPolicies), and
# that call returns policies with their tags stripped - issue #266's opening
# line, and #1046's. So on the per-type leg its marker can only be recovered
# by joining the listed identifier back against the very tag index the
# tagging leg would have read directly. The instance profile above has the
# opposite shape. Between them they cover both ways a type can arrive at
# this routing decision, which is what kept #692's one-type probe from
# speaking for the service in the first place.
resource "aws_iam_policy" "demo" {
  name = "perregion-e2e-demo"

  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect   = "Allow"
      Action   = "s3:GetObject"
      Resource = "*"
    }]
  })

  tags = {
    tofu-estate  = "perregion-e2e"
    tofu-address = "aws_iam_policy.demo"
  }
}
