# GitHub issue #1649: #716's warning on the label shape. A stock-mode create
# whose configured labels already carry the estate label.
resource "test_instance" "foo" {
  metadata {
    name = "foo"
    labels = {
      tofu-estate = "team-estate"
    }
  }
}
