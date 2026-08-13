# Coverage: the registry-ratified IAM half of the second registry-backed
# batch (#40's admission strategy, #44's row-gen tool, issue #26). See
# internal/live/identity/table.go's "Registry-ratified ... second batch"
# comment for the per-type evidence, and for the row-gen proposals this
# batch rejected or deferred (aws_iam_access_key, aws_iam_policy,
# aws_iam_saml_provider, aws_iam_virtual_mfa_device, aws_iam_group).

# Supporting, not coverage: aws_iam_role.support exists only so
# aws_iam_instance_profile.app has a role to carry. It is itself
# client-named-shaped exactly the way live/e2e/estate/'s own aws_iam_role.app
# is, but it is not claimed as a coverage row here — aws_iam_role is already
# covered there and by live/e2e/estates/lambda/iam.tf.
resource "aws_iam_role" "support" {
  name = "tofu-iam-ecr-cohort-role"

  assume_role_policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Effect    = "Allow"
      Principal = { Service = "ec2.amazonaws.com" }
      Action    = "sts:AssumeRole"
    }]
  })

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_iam_role.support"
  }
}

# Coverage: client-named path (aws_iam_instance_profile — identity is the
# name argument, already in config; confirmed against the provider's own
# identity schema, live/survey-full.json, and against the documented import
# command, which sets id to the profile name verbatim). Curated-68 type,
# previously blocked-emulator (floci's iam:GetInstanceProfile omitted Tags,
# the GetRole gap family) — see this cohort's README for the floci
# verification result against the pinned image.
resource "aws_iam_instance_profile" "app" {
  name = "tofu-iam-ecr-cohort-profile"
  role = aws_iam_role.support.name

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_iam_instance_profile.app"
  }
}

# Coverage: marker path (aws_iam_service_linked_role — IAM computes the
# role's name from aws_service_name using its own internal per-service
# convention, not a string transform of any configured argument; the
# documented import ID is the role's ARN, not the bare RoleName the CFN
# registry reports as primaryIdentifier). elasticbeanstalk.amazonaws.com is
# the provider's own documented example service.
resource "aws_iam_service_linked_role" "app" {
  aws_service_name = "elasticbeanstalk.amazonaws.com"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_iam_service_linked_role.app"
  }
}

# Coverage: client-named path (aws_iam_user — identity is the name
# argument, already in config; confirmed against the provider's own
# identity schema and against the documented import command, which sets id
# to the user name verbatim). Issue #26's second named type: floci's
# iam:GetUser now returns Tags on the pinned image, so the earlier
# blocked-emulator note no longer holds — see this cohort's README.
resource "aws_iam_user" "app" {
  name = "tofu-iam-ecr-cohort-user"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_iam_user.app"
  }
}
