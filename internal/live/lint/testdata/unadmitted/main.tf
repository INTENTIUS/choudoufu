# Fixture for RuleUnadmittedType. aws_accessanalyzer_analyzer is a real,
# mapped type in no residue cohort that no batch has yet ratified
# (TestRefusalSilentForTypeInNoCohort below relies on the no-cohort half).
# aws_cloudwatch_event_rule held this place until the Component vocabulary
# gained the omitted-bus fallback (Default) and its batch admitted it; if
# an analyzer batch admits this one too, swap in the next unadmitted
# no-cohort type the same way.

resource "aws_accessanalyzer_analyzer" "web" {
  analyzer_name = "example"
}
