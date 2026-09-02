# A stock configuration file: nothing in here is choudoufu-specific, which is
# the sidecar's whole point - the live configuration lives beside this file in
# estate.chdf.hcl, and stock tooling reading this directory sees only standard
# syntax.
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}
