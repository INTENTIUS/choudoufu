# Coverage: client-named path (aws_dynamodb_table — identity is the table
# name in config; the provider's documented import ID is the name and its id
# attribute equals it). First slice of the survey's client-named cohort (#19).

resource "aws_dynamodb_table" "events" {
  name         = "tofu-stateless-e2e-events"
  billing_mode = "PAY_PER_REQUEST"
  hash_key     = "pk"

  attribute {
    name = "pk"
    type = "S"
  }

  tags = {
    tofu-estate  = local.estate_tag
    tofu-address = "aws_dynamodb_table.events"
  }
}
