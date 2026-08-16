# RuleModuleProviderBlock's admitted case (GitHub issue #201's parity fix to
# #70's original ruling): the calling module block uses none of count,
# for_each, enabled or depends_on, so stock OpenTofu accepts a provider
# block declared inside the child module
# (internal/configs/provider_validation.go:298-312, :592-607 only refuses
# this once one of those four is in play - a fixture combining a
# content-bearing local block with one of them cannot even be built;
# internal/configs' own testdata/config-diagnostics/nested-provider already
# covers that upstream, forked behavior). Nothing here is refused.

resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
}

module "compute" {
  source = "./child"
}
