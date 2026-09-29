# GitHub issue #1185's fixture: a resource type whose provider schema
# carries a `timeouts` block, declared three ways.
#
# held  - the operator's configured delete deadline, reached through a
#         variable rather than a literal so the decode has to go through the
#         static evaluator the way configuredAttrsSeed's does.
# plain - no timeouts block at all. The provider's own declared defaults are
#         the whole answer for this one, and nothing may rewrite them.
# unparseable - a timeouts value that is not a duration. The provider's own
#         ConfigDecode would reject it on the create path with its own
#         error; re-deriving a destroy's meta is not the place to invent one,
#         so this falls back to the declared default too.

variable "hold" {
  type    = string
  default = "20s"
}

resource "stub_ns" "held" {
  name = "held"

  timeouts {
    delete = var.hold
  }
}

resource "stub_ns" "plain" {
  name = "plain"
}

resource "stub_ns" "unparseable" {
  name = "unparseable"

  timeouts {
    delete = "not-a-duration"
  }
}
