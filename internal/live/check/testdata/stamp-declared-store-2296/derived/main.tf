# GitHub issue #2296, the "derived" case. TestOfflineStampHonorsADeclaredStore
# runs this directory and its two siblings with one schema, under which
# aws_kms_alias has nowhere to carry a marker. Its identity is its name, and
# name_prefix leaves the name to the provider, so it is found only by a
# marker or by the record the apply writes. aws_vpc, an earlier choice, did
# not reach the refusal: any record store, the implied one included, lets a
# server-assigned type with an "id" resolve record-located instead
# (identity.RecordFallbackType). aws_kms_alias is barred from that door
# (identity.IDNotProvenWholeTypes) while the apply can still record it from
# its ratified name component (projection.ApplyRecordsIdentity).
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }

  backend "s3" {
    bucket = "stamp-declared-store-2296-state"
    key    = "estate/terraform.tfstate"
    region = "us-east-1"
  }

  live {
    estate = "stamp-declared-store-2296"
  }
}

provider "aws" {
  region = "us-east-1"
}

resource "aws_kms_alias" "unmarkable" {
  name_prefix   = "alias/stamp-declared-store-2296-"
  target_key_id = "1234abcd-12ab-34cd-56ef-1234567890ab"
}
