// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// checkModuleProviderBlocks refuses a provider block declared inside a child
// module, but only when the module tree cannot legally reach it without a
// meta-argument stock OpenTofu itself forbids on the same shape.
//
// GitHub issue #70 first ruled this refused unconditionally: live mode reads
// provider configurations from the root module only, so an in-module
// provider block was never consulted and the module's resources were
// silently served by the root configuration's own provider config instead -
// possibly a different account, region, or set of credentials than the block
// asked for. That measurement was 740 terraform-aws-modules files, all
// "published deployment" origin outside it, and it missed the shape this
// rule was rewritten for: GitHub issue #201 found a real, working corpus
// site - simpleinfra/terraform/shared/modules/gha-iam-user/main.tf:10,
// `provider "github" { owner = var.org }` - reached by callers that use none
// of count, for_each, enabled or depends_on. Stock OpenTofu accepts that
// shape; refusing it was a parity gap, not a correct narrower rule.
//
// internal/configs/provider_validation.go's validateProviderConfigs is what
// stock OpenTofu actually enforces (provider_validation.go:298-312,
// :592-607): a module's own provider block is legal UNLESS the call chain
// from root down to that module passes through a module call using count,
// for_each, enabled or depends_on - at which point OpenTofu hard-errors,
// because such a call needs one provider-graph node per instance and a
// module-local block can supply only one. That condition is inherited down
// through further nesting once it applies to any ancestor call - not just
// the immediate parent's - which is why [checkConfig] threads
// noProviderConfigRange the same way validateProviderConfigs threads its own
// argument of that name, rather than this function looking at only the
// module's immediate caller.
//
// Root module only in the other direction: the root's own provider blocks are
// exactly what live mode does consult, so [checkConfig]'s walk reaches this
// function for every module node and it reports nothing at root. This is
// distinct from [checkModuleProviderMapping] (#104), which polices the
// providers mapping on the module *call*: a module can declare no provider
// block of its own and still be called with a mapping, and vice versa.
func checkModuleProviderBlocks(mod *configs.Module, path addrs.Module, noProviderConfigRange *hcl.Range, issues *[]Issue) {
	if path.IsRoot() || noProviderConfigRange == nil {
		return
	}

	names := make([]string, 0, len(mod.ProviderConfigs))
	for name := range mod.ProviderConfigs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		pc := mod.ProviderConfigs[name]
		if !configuredProviderBlock(pc) {
			// An empty block - no attributes, no version constraint - is
			// upstream's own "could be a proxy configuration" shape
			// (provider_validation.go:320-322,340-351: emptyConfigs, never
			// added to configured). validateProviderConfigs's own
			// count/for_each/enabled/depends_on error only fires when
			// len(configured) > 0, so a call chain that blocks a real,
			// content-bearing local block leaves an empty one alone - it is
			// legal precisely because it carries no divergent settings of
			// its own; [providerscope.Resolve] treats it the same way,
			// falling through to whatever the call chain actually supplies
			// rather than resolving to this block's own (empty) content.
			continue
		}
		block := fmt.Sprintf("provider %q", pc.Name)
		if pc.Alias != "" {
			block = fmt.Sprintf("provider %q, alias %q", pc.Name, pc.Alias)
		}
		*issues = append(*issues, Issue{
			Rule:      RuleModuleProviderBlock,
			Construct: block,
			Module:    path,
			Detail: fmt.Sprintf(
				"%s declares %s, and a module call in the chain that reaches it uses count, for_each, "+
					"enabled or depends_on (at %s). Stock OpenTofu refuses this same combination: a module "+
					"call needing one provider-graph node per instance cannot be served by a single "+
					"module-local provider block. Remove the meta-argument from the call chain, or move the "+
					"provider configuration to the root module and let the module receive it implicitly. A "+
					"providers mapping on the module call stays subject to the module-providers rule's "+
					"admitted shapes: an unaliased mapping such as providers = { aws = aws } is admitted, an "+
					"aliased one is refused",
				path.String(), block, noProviderConfigRange.String(),
			),
			Subject: pc.DeclRange,
		})
	}
}

// moduleCallBlocksLocalProviders reports the source range stock OpenTofu
// itself would cite as the reason a module call cannot lead to a module with
// its own provider block: the range of whichever of count, for_each, enabled
// or depends_on the call uses, checked in exactly that order to match
// internal/configs/provider_validation.go:298-312's switch. Returns nil when
// none of the four is present, meaning this call does not itself forbid a
// local provider block further down its subtree (an ancestor call further
// up the chain still might; see [checkConfig]'s noProviderConfigRange
// threading).
func moduleCallBlocksLocalProviders(call *configs.ModuleCall) *hcl.Range {
	if call == nil {
		return nil
	}
	switch {
	case call.Count != nil:
		return call.Count.Range().Ptr()
	case call.ForEach != nil:
		return call.ForEach.Range().Ptr()
	case call.Enabled != nil:
		return call.Enabled.Range().Ptr()
	case call.DependsOn != nil:
		if len(call.DependsOn) > 0 {
			return call.DependsOn[0].SourceRange().Ptr()
		}
		return call.DeclRange.Ptr()
	}
	return nil
}

// configuredProviderBlock reports whether pc carries real configuration - at
// least one attribute or nested block, or a version constraint - rather than
// being an empty declaration such as `provider "aws" {}`. This is exactly
// internal/configs/provider_validation.go:340-351's own configured/
// emptyConfigs split (mod.ProviderConfigs is that function's own "mod", the
// same field this rule already walks), duplicated here because that
// function's sets are unexported and local to one validation pass. See
// providerscope.Resolve's sibling copy of the same check.
func configuredProviderBlock(pc *configs.Provider) bool {
	_, contentDiags := pc.Config.Content(&hcl.BodySchema{})
	return contentDiags.HasErrors() || pc.Version.Required != nil
}
