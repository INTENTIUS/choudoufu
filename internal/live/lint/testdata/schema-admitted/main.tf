# Fixture for #45: a type outside the v0 admission table (aws_thing is not
# a real provider type), passed to CheckWith with a fake schema whose
# identity is a single required argument, name, plus the AWS context pair as
# optional-for-import. identity.SynthesizeTypeIdentity admits this shape
# outright, so this fixture must produce no issues once schemas are given -
# and RuleUnadmittedType without them, since admitted() only ever grows with
# schemas, never on its own.

resource "aws_thing" "one" {
  name = "widget"
}
