# The command-layer fixture for the data-read phase's provider boundary, in
# both demand classes at once.
#
# data "external" runs a program named by its own arguments on the machine
# running the plan. Here its result reaches BOTH an identity-bearing position
# (the log group's name, which #179's identity read class demands) and a root
# output (which #349's root-output read class demands), so one fixture
# exercises both call sites in internal/command/live_plan.go.
#
# data.aws_region is the control: same run, a provider this estate manages
# objects through, and it must go on being read.
data "external" "naming" {
  program = ["./name.sh"]
}

data "aws_region" "current" {}

resource "aws_cloudwatch_log_group" "named" {
  name = "/logs/${data.external.naming.result["suffix"]}"
}

resource "aws_cloudwatch_log_group" "regional" {
  name = "/logs/${data.aws_region.current.name}"
}

output "suffix" {
  value = data.external.naming.result
}

output "region" {
  value = data.aws_region.current.name
}
