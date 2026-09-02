terraform {
  live {
    estate = "projection-empty-import"
  }

  required_providers {
    examplecloud = {
      source = "example.com/test/examplecloud"
    }
  }
}

# Both identity arguments are set literally, so identity resolves this
# instance CONCRETE. The type is not in DefaultTable, so its entry comes from
# identity.SynthesizeTypeIdentity, and its two client-named identity
# attributes make that entry IdentityObjectOnly - which means the resolution
# carries an empty ImportID on purpose and the identity object is the only
# form there is. What each subtest's identity schema then does with it is in
# projection_layer_test.go.
resource "examplecloud_pair" "one" {
  cluster = "prod"
  service = "web"

  tags = {
    Name = "one"
  }
}
