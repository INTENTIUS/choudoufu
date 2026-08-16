# Limits fixture: RuleModuleProviderBlock, GitHub issue #70's original
# ruling as narrowed by issue #201.
#
# The child module declares a provider block of its own, and the call to it
# below names no count, for_each, enabled or depends_on. #70's original
# ruling refused every in-module provider block outright, on the
# measurement that 0 of 740 module-source files across the ten
# most-installed terraform-aws-modules repositories declare one. #201 found
# a real corpus site using exactly this shape and checked it against
# upstream: stock OpenTofu accepts a module-local provider block UNLESS the
# call chain reaching it uses one of those four meta-arguments (that is
# what internal/configs/provider_validation.go's validateProviderConfigs
# enforces, and this fork forks it verbatim), so refusing it here was a
# parity gap, not a correct narrower rule.
#
# This fixture's shape is that legal, unconditional case, so it is admitted
# today: CheckContext() reports nothing for it. It is also honoured, not
# silently ignored - internal/live/providerscope.Resolve walks straight to
# a module's own content-bearing provider block when the call chain does
# not block it, instead of falling back to the root's configuration, which
# is what closes #70's original "possibly a different account or region,
# with nothing said about it" risk for this shape. The only remaining
# refusal branch (a blocked call chain reaching a content-bearing block)
# cannot be reached through any buildable configuration, in this fork or in
# stock OpenTofu: internal/configs.BuildConfig's own validateProviderConfigs
# hard-errors on that combination before live-mode lint ever runs. See
# internal/live/lint/module_provider_block_test.go's package doc and
# live/LIMITATIONS.md, "module-provider-block", for the fuller account, and
# limits_test.go's notYetEnforcedLimits entry for why this fixture now
# belongs in that bucket rather than in enforcedLimits.

provider "aws" {
  region = "us-west-2"
}

module "compute" {
  source = "./child"
}
