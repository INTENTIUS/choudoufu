# Issue #212: a descendant module's data source (child/main.tf's
# data.test_zone.sub) reads a module-call variable whose value, in THIS
# (the ancestor) module, is itself another data source's attribute. Before
# the fix, the ancestor's own StaticEvaluator - frozen at load time, before
# any caller ever attached a data lookup - had no coverage for
# data.test_zone.root at all, so var.zone_name's value refused as
# unreadable and the whole chain classified ineligible even though every
# link is, on its own, a perfectly ordinary same-stack data source.
data "test_zone" "root" {
  name = "root.example.com."
}

module "child" {
  source     = "./child"
  zone_name  = data.test_zone.root.name
}
