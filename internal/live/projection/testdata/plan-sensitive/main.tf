# A resource one of whose arguments comes from a `sensitive = true` input
# variable, beside a computed attribute the provider derives from a DIFFERENT,
# non-sensitive argument.
#
# internal/configs/static_scope.go marks a sensitive variable's value on the
# way into the static evaluation context, and StaticEvaluator.DecodeBlock -
# unlike its DecodeExpression sibling - has no guard that refuses one. So the
# whole block decodes to a marked value, and planOne's job is to strip the
# marks for the plugin channel and put them back on the answer.
#
# `derived` is the point of the fixture: it is what a caller resolving an
# identity actually wants, it is derived from `name` alone, and nothing about
# it is sensitive. Before the unmark it was unreachable anyway, because the
# whole resource dropped out of the pass.

variable "db_password" {
  type      = string
  sensitive = true
  default   = "hunter2"
}

resource "stub_db" "main" {
  name     = "app-db"
  password = var.db_password
}
