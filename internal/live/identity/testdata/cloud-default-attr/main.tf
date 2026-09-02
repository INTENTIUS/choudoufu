# The Glue catalog_id argument the provider documents as defaulting to the
# caller's own AWS account ID: "If omitted, this defaults to the AWS Account
# ID". Every shape the resolver has to tell apart is here.

variable "cross_account" {
  type    = bool
  default = true
}

variable "peer_catalog" {
  type    = string
  default = "999988887777"
}

# Stated outright: the identity names another account's Data Catalog.
resource "aws_glue_catalog_database" "peer" {
  catalog_id = var.peer_catalog
  name       = "shared"
}

# Stated through a local, which is still stated.
resource "aws_glue_catalog_table" "peer" {
  catalog_id    = var.peer_catalog
  database_name = "shared"
  name          = "events"
}

# Omitted: the account is the documented fallback, and nothing here says
# which account this run is against.
resource "aws_glue_catalog_database" "own" {
  name = "local"
}

# Written but conditionally null, which is an absence rather than a value -
# the same conditional spelling name/name_prefix already has to survive.
resource "aws_glue_catalog_database" "maybe" {
  catalog_id = var.cross_account ? null : var.peer_catalog
  name       = "maybe"
}

# The contrast case: a cloud-bearing component with NO argument beside it.
# The account is a bare segment in the middle of the ARN CloudFront mints, and
# the provider documents no argument that names it, so there is nothing an
# author could set instead and the refusal has no next step to offer.
resource "aws_cloudfront_realtime_log_config" "logs" {
  name = "traffic"
}
