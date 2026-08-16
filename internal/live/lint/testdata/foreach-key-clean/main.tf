# The other side of the for_each key rule: every key here is inside the
# admitted set (letters, digits, space, and + - = . _ : / @ - "." and ":"
# included since issue #178, escaped rather than refused), and the three
# for_each shapes below are the ones the rule must decline to guess at.
#
# aws_route_table_association.this iterates over another resource: its keys
# are aws_subnet.this's keys, checked where they are declared, so checking
# them again here would double-report one finding.

variable "extra_subnets" {
  type    = set(string)
  default = ["from-a-variable"]
}

locals {
  subnets = {
    "a"          = "10.42.1.0/24"
    "b-2"        = "10.42.2.0/24"
    "team_one"   = "10.42.3.0/24"
    "eu/west"    = "10.42.4.0/24"
    "size=1"     = "10.42.5.0/24"
    "plus+one"   = "10.42.6.0/24"
    "at@sign"    = "10.42.7.0/24"
    "with space" = "10.42.8.0/24"
    "0"          = "10.42.9.0/24"
    "été"        = "10.42.10.0/24"
    "alice.smith" = "10.42.11.0/24"
    "2001:db8::/64" = "10.42.12.0/24"
  }
}

resource "aws_subnet" "this" {
  for_each = local.subnets

  cidr_block = each.value
}

resource "aws_route_table_association" "this" {
  for_each = aws_subnet.this

  subnet_id = each.value.id
}

resource "aws_cloudwatch_log_group" "computed" {
  # Built from a data source: not computable in a static scope, so the rule
  # says nothing. Identity resolution refuses this configuration outright,
  # which is where that refusal belongs.
  for_each = toset(data.aws_availability_zones.available.names)

  name = "/tofu/${each.key}"
}

resource "aws_cloudwatch_log_group" "from_variable" {
  # A variable-driven for_each. In a real run the static evaluator has the
  # variable's value and the keys are checked; under a test module call that
  # refuses to supply variables the evaluation panics, and the rule has to
  # degrade to "cannot check" rather than take the run down with it.
  for_each = var.extra_subnets

  name = "/tofu/${each.key}"
}

data "aws_availability_zones" "available" {}
