# GitHub issue #221's proper fix, positive case: aws_route53_record has
# THREE real identity components (zone_id, name, type -
# table_generated.go), and every one of them cascades onto a resource
# whose own identity, in turn, reads the same eligible data source. Before
# this fix, #221's interim rule refused to reclassify ANY dependent with
# more than one real component, unconditionally - it would have refused
# this configuration even though nothing here is actually broken. The
# proper fix evaluates every component, sees that all three failures trace
# back to an eligible read, and reclassifies the whole instance.
data "aws_route53_zone" "primary" {
  name = "example.com."
}

resource "aws_s3_bucket" "zone_id_holder" {
  bucket = "zone-id-${data.aws_route53_zone.primary.zone_id}"
}

resource "aws_s3_bucket" "name_holder" {
  bucket = "name-${data.aws_route53_zone.primary.zone_id}"
}

resource "aws_s3_bucket" "type_holder" {
  bucket = "type-${data.aws_route53_zone.primary.zone_id}"
}

resource "aws_route53_record" "rec" {
  zone_id = aws_s3_bucket.zone_id_holder.bucket
  name    = aws_s3_bucket.name_holder.bucket
  type    = aws_s3_bucket.type_holder.bucket
}
