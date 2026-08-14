# The called module. Nothing here is unusual; the refusal is about how the
# call maps its provider, not about anything the module itself declares.

resource "aws_s3_bucket" "data" {
  bucket = "estate-module-providers"
}
