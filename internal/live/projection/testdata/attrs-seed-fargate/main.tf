# Fixtures for TestConfiguredAttrsSeedFixesTaskDefinitionFormat and
# TestConfiguredAttrsSeedFixesClientSideOnlyDefault (GitHub issues #395 and
# #376): choudoufu keeps no persisted state, so importAndRead's import stub
# is far barer than what an ordinary, state-backed OpenTofu run would hand
# ReadResource - see configuredAttrsSeed's doc comment in build.go.
#
# In a directory of its own (not testdata/attrs-seed) because that name is
# corpus-eks-basic/test_plan's own fixture (stub_lc, the launch-
# configuration shape) - the two units both generalized the same seed
# mechanism independently and reconciled onto one implementation, but each
# kept its own reproduction fixture.

resource "stub_service" "this" {
  name            = "svc"
  task_definition = "arn:aws:ecs:eu-west-1:000000000000:task-definition/mini-td:1"
}

resource "stub_task_definition" "this" {
  family        = "mini-td"
  track_latest  = true
}

# A boundary fixture for configuredAttrsSeed itself: one attribute the
# configuration sets (must be seeded), one it leaves entirely unset (must
# stay unseeded - a null config value carries nothing to seed), and one
# Optional+Computed attribute set to a concrete value anyway (must NOT be
# seeded - Computed means the provider, not configuration, is allowed to
# answer for it, and this rule is asked from the schema flag alone, not
# from whether configuration happens to also supply a value).
resource "stub_widget" "this" {
  name          = "widget-1"
  computed_flag = "operator-supplied"
}
