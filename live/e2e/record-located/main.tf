# The record-located fixture: issue #270's crossing.
#
# Three of the four resources here have nowhere to write an ownership
# marker. aws_cloudfront_public_key and aws_ecr_registry_policy carry no
# tags argument at all, so no tag can say "this estate owns this object" and
# no tag can say WHICH object an instance is. They are in
# identity.MarkerlessTypes for the first reason; issue #270 is about the
# second, which is a different question with a different answer.
#
# The answer is that choudoufu created these objects, so it knew their
# identities at the moment of creation, and it wrote them into the estate's
# record store under the "tofu-located" namespace root. That is what
# identity.ClassRecordLocated means and it is why the live block below MUST
# declare a record_store: without one these types are refused at plan time
# rather than admitted, because an identity nothing can record is an
# identity no later run can recover.
#
# What makes this a measurement rather than an exercise:
#
#   aws_cloudfront_public_key's identity is SERVER-MINTED and appears
#   nowhere in this file. The two keys' import IDs are opaque strings the
#   provider assigns at create time. No re-reading of this configuration can
#   produce one, no tag carries one, and once the state file is deleted the
#   record store is the only thing in the world that knows which live key
#   "rl-e2e-alpha" is. run.sh asserts the rendered identity against the
#   EMULATOR's own answer - it asks CloudFront which id has the name
#   rl-e2e-alpha and requires the run to have rendered that id - so a record
#   pointing at the wrong object fails even though both objects exist and
#   both ids are well-formed.
#
#   aws_ecr_registry_policy is the opposite identity shape in the same
#   class: an account singleton whose import ID is the registry id. It is
#   here so the fixture is not a proof about one shape. A mechanism that
#   worked only for opaque server-minted ids would pass with the public keys
#   alone.
#
#   aws_vpc is what makes the negative claims above measurable. Its identity
#   is server-assigned AND it is taggable, so it is a needs-discovery type:
#   the run must sweep for it by marker, which means the "Foreign resources"
#   line reports a sweep that HAPPENED. Without it every type here is
#   client-named or located, no sweep runs at all, and "the located objects
#   were not proposed for destruction" would be true of a run that looked at
#   nothing - which is not the same statement.

terraform {
  required_version = ">= 1.5.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.59.0"
    }
  }

  live {
    estate = "record-located-e2e"

    # The migration step, stated. An implicit default would mean an estate
    # silently acquires a local file whose loss causes a duplicate create,
    # which is close enough to the state file issue #73 removes. Declaring
    # it is what admits the three markerless resources below.
    record_store "local" {
      path = ".tofu-records"
    }
  }
}

provider "aws" {
  region                      = "us-east-1"
  access_key                  = "test"
  secret_key                  = "test"
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  s3_use_path_style           = true
}

locals {
  # Two real RSA-2048 public keys. CloudFront rejects anything that is not
  # RSA-2048 or ECDSA P-256 in X.509 PEM form - the emulator enforces this
  # too - so these cannot be shortened into placeholders.
  #
  # They are DIFFERENT keys on purpose. If the two records were ever swapped
  # the encoded_key would disagree with the live object as well, so the
  # fixture has a second, independent way to notice; run.sh's mutation step
  # relies on the identity assertion alone and checks the plan separately.
  keys = {
    "rl-e2e-alpha" = <<-EOT
      -----BEGIN PUBLIC KEY-----
      MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEAvQ63t5JnB+OwXUCwG1fI
      K5GSbEo/f1qdFRAI7P/x37h8HS/p2whgXN8GXowu2oHpu5P/K/eGJfabFnEOYsVw
      APZ2vW8GlBhhXyQMsM14LmEJqO85iADW2Jh7J847MApzMAz9VLSH6Dain1MNzaLh
      RpKxWd4qRSI6nDu0H+8bMa+rRi9oBkZgMBGpNQVb1FKYbdEw2ZULXdnQgoFmAQe1
      WcD0ZtmBKudvjIeTqeduZEMfrhX2gNZ0f6q01zaQc/OYbX5q9cAt2CbhHDZW6R6i
      OCI4zKFxblJpbd8nN3fTETb1UTOb8ynRbaafkRtPZGTv+cHherPFBSKIahqpqJXN
      SQIDAQAB
      -----END PUBLIC KEY-----
    EOT
    "rl-e2e-bravo" = <<-EOT
      -----BEGIN PUBLIC KEY-----
      MIIBIjANBgkqhkiG9w0BAQEFAAOCAQ8AMIIBCgKCAQEArkbZRdi44sgw611VopQy
      XZDWKaixyj58iXYrmkrGqs5v6SQNBeRNvEFUjjvjj3eXuJQyYftrWIT2LeZEnwbC
      n9fWtEV3QJLvs+WiTmELM4MA7th3cfQwLiJ6gFOfx8Vvdqvkga/QmNgHpNVY6WiP
      LVd9VVhg+OsNtk3SM65RH3szCIMAiBRvQt3BS8C143r6ZpcbG4yTIA2CHypb0zgw
      ualDGuM8pEGEAoEVEv+1/33u2w4bEjUnrejFh6MBRNhwmnGQ5b+ib++x+It1JZqX
      bzHYEx1btRxikar+JKB/LG8FzyOIuD7Phw3gKmJJo/rmSR1sqAJ2sgqOPnysk/uK
      gQIDAQAB
      -----END PUBLIC KEY-----
    EOT
  }
}

# Record-located. The provider mints the id; nothing here can predict it.
resource "aws_cloudfront_public_key" "signers" {
  for_each = local.keys

  name        = each.key
  comment     = "record-located e2e ${each.key}"
  encoded_key = each.value
}

# Record-located, and an account singleton: the import id is the registry
# id, so this one's identity IS derivable from the account. It is here to
# show the located path does not depend on the identity being opaque.
resource "aws_ecr_registry_policy" "registry" {
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = "rlE2eReplicate"
      Effect    = "Allow"
      Principal = { AWS = "arn:aws:iam::000000000000:root" }
      Action    = ["ecr:ReplicateImage"]
      Resource  = "*"
    }]
  })
}

# Taggable with a server-assigned id, so this is the one type here that
# NEEDS marker discovery. It is the control: it gives the sweep something to
# find, so the "Foreign resources" line below reports a sweep that ran.
resource "aws_vpc" "control" {
  cidr_block = "10.222.0.0/16"

  tags = {
    tofu-estate  = "record-located-e2e"
    tofu-address = "aws_vpc.control"
  }
}
