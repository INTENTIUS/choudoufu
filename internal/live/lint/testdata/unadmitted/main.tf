# Fixture for RuleUnadmittedType. aws_api_gateway_deployment is a real,
# mapped type in no residue cohort that no batch has yet ratified
# (TestRefusalSilentForTypeInNoCohort below relies on the no-cohort half).
# aws_cloudwatch_event_rule held this place until the Component vocabulary
# gained the omitted-bus fallback (Default) and its batch admitted it;
# aws_accessanalyzer_analyzer held it next, until its single-argument
# client-named row was ratified from the import grammar. If a batch admits
# this one too, swap in the next unadmitted no-cohort type the same way.

resource "aws_api_gateway_deployment" "web" {
  rest_api_id = "abcde12345"
}
