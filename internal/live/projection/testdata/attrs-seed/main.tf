# configuredAttrsSeed's fixture: a resource shaped like
# aws_launch_configuration's user_data/user_data_base64 pair (found crossing
# corpus-eks-basic - see build.go's own doc comment on the function). One
# flat, non-identity, non-tags attribute set statically (must be seeded);
# one set from a sibling managed resource's own Computed attribute, which
# the static evaluator cannot resolve (must be left alone, exactly the way
# configuredTagsSeed leaves a non-static tags argument alone); "tags",
# which configuredTagsSeed already owns and this mechanism must not touch a
# second time; and "id", the identity attribute, which must never be
# seeded from configuration.

resource "stub_lc" "main" {
  name              = "workers"
  user_data_base64  = base64encode("hello world")
  tags = {
    Owner = "alice@example.com"
  }
}

resource "stub_lc" "dynamic" {
  name              = "dynamic-workers"
  user_data_base64  = stub_lc.main.id
}
