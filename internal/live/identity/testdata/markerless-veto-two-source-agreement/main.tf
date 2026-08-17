# markerless-veto-two-source-agreement (issue #274): three types the
# markerless veto used to refuse outright, because row-gen's classifier
# read them server-assigned (tryOpaqueOverride, over one import example
# that does not demonstrate the composite CloudFormation itself names).
# CloudFormation's registry model and the provider's own import
# documentation, read independently of that classifier bucket, agree the
# identity of each is built from configuration - see
# tools/row-gen/annotations.json's own entries for the evidence. This pins
# the RENDERED import identity string against the provider's own
# documented example, not just the resolution verdict.

# Doc: "Import using the user pool ID and Client ID separated by a `:`" -
# example "example:example".
resource "aws_cognito_risk_configuration" "main" {
  user_pool_id = "us-east-1_abc123"
  client_id    = "1example23456789"
}

# Doc: "using the ARN of the graph followed by the account ID of the member
# account" - example
# "arn:aws:detective:us-east-1:123456789101:graph:231684d34gh74g4bae1dbc7bd807d02d/123456789012".
resource "aws_detective_member" "member" {
  graph_arn     = "arn:aws:detective:us-east-1:123456789101:graph:231684d34gh74g4bae1dbc7bd807d02d"
  account_id    = "123456789012"
  email_address = "member@example.com"
}

# Doc: "Name with qualifier" - example "example:production".
resource "aws_lambda_function_event_invoke_config" "main" {
  function_name = "example"
  qualifier     = "production"
}
