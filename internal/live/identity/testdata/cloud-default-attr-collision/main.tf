# The same catalog and the same name from two blocks: one live object,
# claimed twice. A cloud-bearing component that now reads an argument must
# still feed checkCollisions the value it read.

resource "aws_glue_catalog_database" "clash_a" {
  catalog_id = "111122223333"
  name       = "clash"
}

resource "aws_glue_catalog_database" "clash_b" {
  catalog_id = "111122223333"
  name       = "clash"
}
