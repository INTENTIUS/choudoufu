# Fixture for TestResidueSeedForFixesAManagedReferenceAttribute (GitHub
# issue #395's own real shape, reduced): stub_service.this's task_definition
# is a reference to ANOTHER resource's computed attribute, which
# configs.StaticEvaluator (configuredAttrsSeed's only source) cannot ever
# resolve - the config-language subset is var/local/path/terminal, never a
# managed resource. This is exactly why residueSeedFor exists: only the
# residue record left over from an earlier migrate or apply can supply this
# attribute's seed.

resource "stub_task_definition" "this" {
  family = "mini-td"
}

resource "stub_service" "this" {
  name            = "svc"
  task_definition = stub_task_definition.this.arn
}
