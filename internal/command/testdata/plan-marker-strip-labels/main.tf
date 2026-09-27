# GitHub issue #1649's fixture: #613's, on the Kubernetes object-metadata
# shape. The configuration declares its own label and knows nothing about
# the tofu-estate label "choudoufu live-import" wrote onto the live object.
resource "test_instance" "foo" {
  metadata {
    name = "foo"
    labels = {
      team = "a"
    }
  }
}
