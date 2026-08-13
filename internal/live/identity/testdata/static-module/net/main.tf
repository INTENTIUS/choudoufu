resource "aws_s3_bucket" "data" {
  bucket = "tofu-stateless-static-module-net-data"
}

resource "aws_s3_bucket_policy" "data" {
  bucket = aws_s3_bucket.data.id
  policy = "{}"
}

module "inner" {
  source = "./inner"
}
