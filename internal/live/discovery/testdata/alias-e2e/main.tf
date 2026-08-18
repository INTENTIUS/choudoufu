# Two aws provider configurations, the default and one aliased "west", each
# pointed at its own region (versions.tf), with two resources on each side.
# The pairs answer different questions and both are needed.
#
# aws_s3_bucket is CLIENT-NAMED: its identity is the "bucket" argument, so it
# resolves out of the configuration and never needs marker discovery at all.
# That pair is issue #64/#69's question - does a plan over resources from two
# aliased provider configurations work at all - asked without any discovery
# in the picture.
#
# aws_vpc is SERVER-ASSIGNED: nothing in the configuration says which vpc-...
# it is, so both instances are ClassNeedsDiscovery and can only be found by
# listing for their markers. That pair is issue #283's question, and it is the
# one this fixture could not ask before: live-plan used to refuse outright
# whenever the resources WAITING ON marker discovery spanned more than one
# provider configuration ("Marker discovery across several provider
# configurations"), on the reasoning that a list issued against the wrong
# region reports an estate as missing rather than as unreachable. Discovery
# now runs one pass per provider configuration with
# discovery.Request.ScopeProvider narrowing each to the resolutions whose own
# resource block names it, so each VPC is looked for only in the region its
# own block names.
#
# The VPCs are also what makes this fixture's region claim real. The test's
# own comment on s3api list-buckets says so: bucket names are account-global
# and both regions list both buckets, so the S3 pair cannot tell a
# region-scoped list from a region-blind one. ec2 DescribeVpcs is
# region-scoped, so us-east-1 sees exactly one of these and us-west-2 sees
# exactly the other - which is what turns "each VPC bound" into "each VPC
# bound through its own provider configuration".

resource "aws_s3_bucket" "east" {
  bucket = "tofu-alias-e2e-east"

  tags = {
    tofu-estate  = "alias-e2e"
    tofu-address = "aws_s3_bucket.east"
  }
}

resource "aws_s3_bucket" "west" {
  provider = aws.west
  bucket   = "tofu-alias-e2e-west"

  tags = {
    tofu-estate  = "alias-e2e"
    tofu-address = "aws_s3_bucket.west"
  }
}

resource "aws_vpc" "east" {
  cidr_block = "10.60.0.0/16"

  tags = {
    tofu-estate  = "alias-e2e"
    tofu-address = "aws_vpc.east"
  }
}

resource "aws_vpc" "west" {
  provider   = aws.west
  cidr_block = "10.61.0.0/16"

  tags = {
    tofu-estate  = "alias-e2e"
    tofu-address = "aws_vpc.west"
  }
}
