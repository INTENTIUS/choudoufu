# Type choice, recorded per the issue's instructions.
#
# The issue's own documented example is aws_efs_file_system (#38): taggable,
# registry-listable, mapped, and with no native provider list resource, so
# EFS is exactly the type this batch exists to make discoverable. It is not
# used here because floci does not serve EFS at all - its
# /_localstack/health services list (floci 1.6.0, floci-always-free
# community edition, checked 2026-08-12) carries no "efs" entry, so no
# aws_efs_file_system could ever be created against it in the first place.
#
# aws_glue_registry (AWS::Glue::Registry) is the least exotic type floci
# does serve that also clears the same three bars: taggable, mapped
# (live/mapping.json, via "name"), and registry-listable with no required
# input (live/registry.json). "glue" reports "running" in floci's health
# endpoint, and `aws glue create-registry` against it succeeds.
#
# What is NOT proven yet - and is the reason the test built around this
# fixture skips rather than asserts a hard pass today - is that floci's
# Cloud Control implementation actually reflects a glue registry created
# through the ordinary service API. Manual probing (same date) found the
# opposite for every non-EC2-default candidate tried: `aws cloudcontrol
# list-resources --type-name AWS::Glue::Registry` returns an empty
# ResourceDescriptions list immediately after create-registry succeeds, and
# `get-resource` on the created registry's name answers UnsupportedOperation
# - the same shape TestCloudControlUnsupportedOperationFallback's fake
# server exercises, except here the ListResources side of the fallback also
# comes back empty rather than finding the resource. The same probe against
# AWS::Route53::HealthCheck, AWS::EC2::KeyPair and AWS::IoT::ThingType found
# the identical pattern; only AWS::EC2::VPC's pre-seeded default VPC came
# back from ListResources at all. See cloudcontrol_live_test.go's skip
# message for the live version of this finding.

resource "aws_glue_registry" "demo" {
  registry_name = "cc-e2e-demo"

  tags = {
    tofu-estate  = "cc-e2e"
    tofu-address = "aws_glue_registry.demo"
  }
}
