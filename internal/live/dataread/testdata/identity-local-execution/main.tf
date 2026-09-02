# The shape an adversarial audit found reachable on 2026-08-21: data
# "external" runs a program named by its own arguments on the machine running
# the plan, and its result lands in an identity-bearing position, so #179's
# identity read class demands it and - before the boundary covered that class
# - read it, which is to say ran the program, before discovery and before
# anything in the run could stop it.
data "external" "naming" {
  program = ["./name.sh"]
}

# The control in the same fixture. The test provider serves managed resource
# types (its schema declares test_thing), so it is an infrastructure provider
# even though THIS configuration manages nothing through it, and the identity
# class must go on reading it: stock OpenTofu plans this without complaint and
# nothing here can run locally.
data "test_zone" "a" {
  name = "example.com."
}

resource "aws_cloudwatch_log_group" "named" {
  name = "/logs/${data.external.naming.result["bucket"]}"
}

resource "aws_cloudwatch_log_group" "zoned" {
  name = "/logs/${data.test_zone.a.zone_id}"
}
