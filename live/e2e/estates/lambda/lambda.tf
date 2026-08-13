# Coverage: the registry-ratified Lambda batch (#40's registry-backed
# admission strategy, #44's row-gen tool). Every resource below is one of
# the five types this batch ratified into admittedTypesV0
# (internal/live/lint/admission.go) and DefaultTable
# (internal/live/identity/table.go) — see table.go's "Registry-ratified"
# section comment for the per-type evidence, and for the two row-gen
# proposals (aws_lambda_alias, aws_lambda_layer_version_permission) this
# batch rejected and left out of both tables.

# Coverage: client-named path (aws_lambda_capacity_provider — identity is
# the name argument, already in config; confirmed against the provider's
# own identity schema, live/survey-full.json). Literal placeholder
# subnet/security-group IDs rather than real aws_subnet/aws_security_group
# resources: this cohort exercises Lambda identity admission, not EC2
# networking, and both types are already covered by live/e2e/estate/.
resource "aws_lambda_capacity_provider" "app" {
  name = "tofu-lambda-cohort-capacity"

  vpc_config {
    subnet_ids         = ["subnet-0123456789abcdef0"]
    security_group_ids = ["sg-0123456789abcdef0"]
  }

  permissions_config {
    capacity_provider_operator_role_arn = aws_iam_role.lambda.arn
  }

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_lambda_capacity_provider.app"
  }
}

# Coverage: marker path (aws_lambda_code_signing_config — Lambda mints the
# config's ARN at create time; the type has no name argument for a wrong
# guess to reach for). The signing profile is a literal placeholder ARN
# rather than a real aws_signer_signing_profile resource, which is outside
# this batch's scope.
resource "aws_lambda_code_signing_config" "app" {
  allowed_publishers {
    signing_profile_version_arns = [
      "arn:aws:signer:us-east-1:000000000000:/signing-profiles/tofu_lambda_cohort/1a2b3c4d5e",
    ]
  }

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_lambda_code_signing_config.app"
  }
}

# Coverage: client-named path (aws_lambda_function — identity is the
# function_name argument, already in config; confirmed against the
# provider's own identity schema). Image-packaged so the fixture needs no
# local zip artifact or S3 object.
resource "aws_lambda_function" "app" {
  function_name = "tofu-lambda-cohort-app"
  role          = aws_iam_role.lambda.arn
  package_type  = "Image"
  image_uri     = "000000000000.dkr.ecr.us-east-1.amazonaws.com/tofu-lambda-cohort-app:latest"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_lambda_function.app"
  }
}

# Coverage: marker path (aws_lambda_event_source_mapping — Lambda mints
# the mapping's UUID at create time; the event_source_arn below names what
# it reads from, not the mapping itself). The source is a literal stream
# ARN rather than a real aws_dynamodb_table resource with streaming
# enabled — the same "keep the block out of the emulator's boundary"
# choice live/e2e/estate/iam.tf makes for its inline policy's bucket ARN.
# DynamoDB Streams is not what this cohort is testing.
resource "aws_lambda_event_source_mapping" "app" {
  event_source_arn  = "arn:aws:dynamodb:us-east-1:000000000000:table/tofu-lambda-cohort-events/stream/2026-01-01T00:00:00.000"
  function_name     = aws_lambda_function.app.function_name
  starting_position = "LATEST"

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_lambda_event_source_mapping.app"
  }
}

# Coverage: marker path (aws_lambda_layer_version — untaggable; carries no
# tags argument in the provider, so it is not swept for removal like the
# twelve untaggable types live/e2e/estate/README.md names — see this
# cohort's own README, "Untaggable types", for why that entry cannot land
# in live/LIMITATIONS.md yet. Lambda mints the layer version's ARN,
# embedding a version number it assigns and increments itself; layer_name
# names the family, not one immutable version of it). s3_bucket/s3_key are
# literal placeholders, the same choice the capacity provider's signing
# profile ARN makes, rather than a real aws_s3_object this batch has no
# reason to admit.
resource "aws_lambda_layer_version" "app" {
  layer_name          = "tofu-lambda-cohort-layer"
  s3_bucket           = "tofu-lambda-cohort-artifacts"
  s3_key              = "layers/app.zip"
  compatible_runtimes = ["python3.13"]
}
