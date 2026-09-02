// Only one quadrant set; the other three, tag_key/tag_value, scope and
// threshold are all left to their defaults.
terraform {
  live {
    estate = "my-estate"
    policy {
      declared_tagged = "keep"
    }
  }
}
