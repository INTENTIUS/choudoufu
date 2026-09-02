# Fixture for RuleUnadmittedType. aws_athena_capacity_reservation is a real,
# mapped type in no residue cohort that no batch has yet ratified
# (TestRefusalSilentForTypeInNoCohort below relies on the no-cohort half).
# aws_cloudwatch_event_rule held this place until the Component vocabulary
# gained the omitted-bus fallback (Default) and its batch admitted it;
# aws_accessanalyzer_analyzer held it next, until its single-argument
# client-named row was ratified from the import grammar;
# aws_api_gateway_deployment held it until issue #309 widened the markerless
# veto to a partly read-only primary identifier, which moved it from an
# unadmitted-type refusal to a markerless-type one. If a batch admits this
# one too, swap in the next unadmitted no-cohort type the same way.
#
# One property this replacement has that the last three did not, and it is
# the reason for choosing it: it is TAGGABLE. The markerless veto's first
# clause is untaggability, so no widening of that veto can ever
# reach this fixture the way it reached its predecessor - only a ratification
# batch can, which is the one move the header above already tells you to
# make.

resource "aws_athena_capacity_reservation" "web" {
  name = "example-rule"
}
