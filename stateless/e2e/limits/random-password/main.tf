# Limits fixture: RuleLogicalResource, random_password.
#
# The generated value is stored once and never regenerated; that stored value
# is the store stateless mode removes, and nothing about the live system can
# tell a re-run what the value was. See stateless/LIMITATIONS.md.

resource "random_password" "db" {
  length = 16
}
