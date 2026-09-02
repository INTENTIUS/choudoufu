variable "name" {
  type = string
}

resource "aws_s3_bucket" "this" {
  bucket = "${var.name}-store"
}

output "bucket_name" {
  value = "${var.name}-store"
}

output "bucket_id" {
  value = aws_s3_bucket.this.bucket
}
