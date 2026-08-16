# The adversarial case for issue #212's fix: two modules each declare a
# data source at the SAME address (data.test_zone.shared), with different
# values. The child's own module-call variable carries the ROOT's value
# across the boundary; the child's OWN same-named declaration is never
# referenced by anything and must never be consulted in its place. A
# module-scoped data lookup that got the "asking module" wrong here would
# feed the child's own "child-wrong.com." into a read that should only ever
# see the root's "root-real.com." - a wrong provider read, not merely a
# wrong refusal.
data "test_zone" "shared" {
  name = "root-real.com."
}

module "child" {
  source    = "./child"
  zone_name = data.test_zone.shared.name
}
