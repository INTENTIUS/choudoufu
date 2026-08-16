# A child module's output feeding an identity argument. Every reference
# here is one stock OpenTofu evaluates without complaint; before this
# fixture, all of them refused with "Module output not supported in static
# context" because internal/configs' static scope has no module tree to
# enter and identity resolution never looked past it.

module "bucket" {
  source = "./bucket"
  name   = "app-assets"
}

module "role" {
  source = "./role"
}

# The plain case: an output defined as a literal-derived expression.
resource "aws_s3_bucket_policy" "direct" {
  bucket = module.bucket.bucket_name
  policy = "{}"
}

# The output is defined as a resource attribute inside the child, so the
# reference resolves through parentPart exactly as a direct sibling
# reference would - under the same identity-attribute restriction.
resource "aws_s3_bucket_versioning" "via_resource" {
  bucket = module.bucket.bucket_id
}

# Reached through a local rather than written at the identity argument, so
# selectStatic's chase has to carry the module hop too.
locals {
  role_name = module.role.name
}

resource "aws_iam_role_policy" "via_local" {
  name = "inline"
  role = local.role_name
}

# Selecting a key out of an output that is an object: the steps after the
# output name are applied to the output's own static shape.
resource "aws_iam_role_policy" "via_object_output" {
  name = "second"
  role = module.role.names.primary
}
