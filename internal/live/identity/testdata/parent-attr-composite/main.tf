# The smallest configuration that exhibits the substitution this fixture
# exists to pin down.
#
# aws_volume_attachment's import identity is DEVICE_NAME:VOLUME_ID:INSTANCE_ID
# and its table entry lists all three names in IdentityAttrs, so a reference to
# any one of them used to be answered with the whole colon-joined string. The
# entry's own components say each of the three supplies one third of it, and
# that is the answer a child has to receive.
#
# The bucket policy is a stand-in for any child whose identity argument is a
# free-form string: what is under test is which value a parent attribute
# yields, not whether an attachment plausibly names a bucket.

resource "aws_volume_attachment" "data" {
  device_name = "/dev/sdh"
  volume_id   = "vol-0123456789abcdef0"
  instance_id = "i-0123456789abcdef0"
}

resource "aws_s3_bucket_policy" "data" {
  bucket = aws_volume_attachment.data.volume_id
  policy = "{}"
}
