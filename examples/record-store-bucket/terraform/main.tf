# The bucket a live estate's record_store "s3" writes to, with the hardening
# S3Store deliberately does not do for you.
#
# S3Store's own doc is explicit: "Bucket creation, lifecycle policy, and
# encryption configuration are the caller's concern." That is the right seam
# for the store — it keeps its surface free of anything AWS-credential-shaped
# — and the wrong place to leave an operator, because records can carry
# secret material: the envelope has `sensitive_attributes` and `private`
# members, and `strict { secrets = "store" }` is the default.
#
# So this root is that obligation discharged, and it is meant to be read as
# documentation as much as run as code. GitHub issue #1244.

terraform {
  required_version = ">= 1.6"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "~> 6.0"
    }
  }
}

provider "aws" {
  region = var.region
}

variable "region" {
  description = "Region the bucket and its key live in. Records are read on every plan, so this wants to be the region the estate is in."
  type        = string
  default     = "us-east-2"
}

variable "bucket" {
  description = "Bucket name. Globally unique, so this has no sensible default."
  type        = string
}

variable "estate_role_arns" {
  description = <<-EOT
    Principals allowed to read and write records under this bucket.

    Empty means the bucket policy grants nobody: the only access is whatever
    IAM already allows, which is the safe default for a first apply. Fill it
    in once you know which role your runs assume.
  EOT
  type        = list(string)
  default     = []
}

# ── The key ────────────────────────────────────────────────────────────────
#
# A customer-managed key rather than SSE-S3, because the point of this
# example is that the protection is auditable and revocable: a CMK has its
# own key policy, its own grants, and its own CloudTrail entries, and turning
# it off makes every record unreadable in one action. SSE-S3 is a legitimate
# simpler choice and the bucket below accepts it with one edit — see README.
resource "aws_kms_key" "records" {
  description             = "choudoufu record store: ${var.bucket}"
  enable_key_rotation     = true
  deletion_window_in_days = 30
}

resource "aws_kms_alias" "records" {
  name          = "alias/${var.bucket}-records"
  target_key_id = aws_kms_key.records.key_id
}

# ── The bucket ─────────────────────────────────────────────────────────────

resource "aws_s3_bucket" "records" {
  bucket = var.bucket
}

# Versioning is ON, and the reason is specific rather than habitual.
#
# S3Store's compare-and-swap is ETag-based (If-Match / If-None-Match), and a
# record's delete is how a tombstone is written. Versioning means a delete
# places a marker rather than destroying history, so a mistaken sweep is
# recoverable and a conditional write still sees the current object's ETag.
# The lifecycle rule below is what stops that history growing without bound.
resource "aws_s3_bucket_versioning" "records" {
  bucket = aws_s3_bucket.records.id
  versioning_configuration {
    status = "Enabled"
  }
}

resource "aws_s3_bucket_server_side_encryption_configuration" "records" {
  bucket = aws_s3_bucket.records.id
  rule {
    apply_server_side_encryption_by_default {
      sse_algorithm     = "aws:kms"
      kms_master_key_id = aws_kms_key.records.arn
    }
    # Records are many and small, and every plan reads the whole namespace.
    # A bucket key collapses the per-object KMS calls that would otherwise
    # cost real money at ten thousand objects.
    bucket_key_enabled = true
  }
}

resource "aws_s3_bucket_public_access_block" "records" {
  bucket                  = aws_s3_bucket.records.id
  block_public_acls       = true
  block_public_policy     = true
  ignore_public_acls      = true
  restrict_public_buckets = true
}

# Expire old VERSIONS, never current objects.
#
# A record is not a log. Expiring a current object would delete an estate's
# identity for a resource that still exists, and the next plan would propose
# creating something already there. What is safe to expire is superseded
# versions, which exist only because versioning is on above.
resource "aws_s3_bucket_lifecycle_configuration" "records" {
  bucket     = aws_s3_bucket.records.id
  depends_on = [aws_s3_bucket_versioning.records]

  rule {
    id     = "expire-superseded-record-versions"
    status = "Enabled"
    filter {}
    noncurrent_version_expiration {
      noncurrent_days = 30
    }
    abort_incomplete_multipart_upload {
      days_after_initiation = 7
    }
  }
}

# ── The policy that makes the encryption real ──────────────────────────────
#
# Default encryption is a default: a client that asks for something else, or
# for none, gets it. These two statements are what turn "the bucket is
# encrypted" from advisory into enforced, and they are the part most often
# missing from a hand-written setup.
data "aws_iam_policy_document" "records" {
  statement {
    sid    = "DenyUnencryptedPuts"
    effect = "Deny"
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.records.arn}/*"]
    condition {
      test     = "StringNotEquals"
      variable = "s3:x-amz-server-side-encryption"
      values   = ["aws:kms"]
    }
  }

  statement {
    sid    = "DenyWrongKey"
    effect = "Deny"
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    actions   = ["s3:PutObject"]
    resources = ["${aws_s3_bucket.records.arn}/*"]
    condition {
      test     = "StringNotEquals"
      variable = "s3:x-amz-server-side-encryption-aws-kms-key-id"
      values   = [aws_kms_key.records.arn]
    }
  }

  statement {
    sid    = "DenyInsecureTransport"
    effect = "Deny"
    principals {
      type        = "*"
      identifiers = ["*"]
    }
    actions   = ["s3:*"]
    resources = [
      aws_s3_bucket.records.arn,
      "${aws_s3_bucket.records.arn}/*",
    ]
    condition {
      test     = "Bool"
      variable = "aws:SecureTransport"
      values   = ["false"]
    }
  }

  # Least privilege for the runs themselves, scoped to the record prefix.
  #
  # This is the bucket-side mirror of what `aws:ResourceTag/tofu-estate` does
  # for the resources: an estate reaches its own records and no other
  # estate's. Narrow the prefix per estate if one bucket serves several.
  dynamic "statement" {
    for_each = length(var.estate_role_arns) > 0 ? [1] : []
    content {
      sid    = "EstateRunsReadWriteTheirOwnRecords"
      effect = "Allow"
      principals {
        type        = "AWS"
        identifiers = var.estate_role_arns
      }
      actions = [
        "s3:GetObject",
        "s3:PutObject",
        "s3:DeleteObject",
      ]
      resources = ["${aws_s3_bucket.records.arn}/choudoufu/*"]
    }
  }

  dynamic "statement" {
    for_each = length(var.estate_role_arns) > 0 ? [1] : []
    content {
      sid    = "EstateRunsListTheirOwnRecords"
      effect = "Allow"
      principals {
        type        = "AWS"
        identifiers = var.estate_role_arns
      }
      # ListObjectsV2 is a bucket-level action, so it cannot be scoped by
      # object key directly — the prefix condition is what scopes it.
      actions   = ["s3:ListBucket"]
      resources = [aws_s3_bucket.records.arn]
      condition {
        test     = "StringLike"
        variable = "s3:prefix"
        values   = ["choudoufu/*"]
      }
    }
  }
}

resource "aws_s3_bucket_policy" "records" {
  bucket     = aws_s3_bucket.records.id
  policy     = data.aws_iam_policy_document.records.json
  depends_on = [aws_s3_bucket_public_access_block.records]
}

output "bucket" {
  description = "Pass this as RECORD_STORE_BUCKET, or as the record_store block's `bucket`."
  value       = aws_s3_bucket.records.id
}

output "kms_key_arn" {
  description = "The key every record is encrypted under. Revoking it makes the store unreadable."
  value       = aws_kms_key.records.arn
}

output "record_store_block" {
  description = "The live block to paste into the estate this bucket serves."
  value       = <<-EOT
    record_store "s3" {
      bucket     = "${aws_s3_bucket.records.id}"
      key_prefix = "choudoufu/<estate>"
      region     = "${var.region}"
    }
  EOT
}
