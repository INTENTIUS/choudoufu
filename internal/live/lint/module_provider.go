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
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// CheckModuleProviders walks cfg's whole module tree and warns about every
// provider block declared inside a child module.
//
// This is GitHub issue #70's interim half. Per-module provider resolution is
// a deferred design question - support it, or lint-refuse it outright - and
// until that is decided, an in-module provider block (aliased or default) is
// neither supported nor refused: it is simply never consulted. Every child
// module's resources are served by the root configuration's own provider
// config instead, exactly as if the module declared no provider block at all
// (statelessProviders.ConfiguredProvider in internal/command/live_plan.go
// documents this as today's actual behavior). An estate whose modules
// declare their own provider blocks - legacy practice upstream guidance
// itself now discourages for shared modules - can misattribute which
// account, region, or credentials a resource is actually planned and applied
// against, with nothing said about it before this check existed.
//
// [Issue] and [Diagnostics] cannot carry this warning: every issue lint
// raises is fatal ([Diagnostics] hardcodes hcl.DiagError), and this is
// deliberately not fatal - the deferred design might still decide to support
// the block rather than refuse it, so refusing it here would be a decision
// this package has not been asked to make. It is wired through the same
// tfdiags channel GitHub issue #63's provider-version-skew warning uses
// instead ([providerversion.Check] in internal/live/providerversion), in the
// same once-per-run style: one call from each live-mode entry point, right
// beside its [CheckWith] call, over the same *configs.Config.
//
// A configuration with no child modules, or whose child modules declare no
// provider block, returns nil: silence is the answer for every fixture
// admitted today, since no fixture in this repository declares one.
func CheckModuleProviders(cfg *configs.Config) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	walkModuleProviders(cfg, &diags)
	return diags
}

// walkModuleProviders is [CheckModuleProviders]'s recursive step. The root
// module's own provider blocks are exactly what live mode does consult, so
// only modules below it are inspected; the walk still visits every module in
// the tree, so a configuration several levels deep gets every offending
// block named in one pass rather than one per fix-and-rerun cycle.
func walkModuleProviders(cfg *configs.Config, diags *tfdiags.Diagnostics) {
	if cfg == nil || cfg.Module == nil {
		return
	}

	if !cfg.Path.IsRoot() {
		names := make([]string, 0, len(cfg.Module.ProviderConfigs))
		for name := range cfg.Module.ProviderConfigs {
			names = append(names, name)
		}
		sort.Strings(names)

		for _, name := range names {
			*diags = diags.Append(moduleProviderWarning(cfg.Path, cfg.Module.ProviderConfigs[name]))
		}
	}

	childNames := make([]string, 0, len(cfg.Children))
	for name := range cfg.Children {
		childNames = append(childNames, name)
	}
	sort.Strings(childNames)
	for _, name := range childNames {
		walkModuleProviders(cfg.Children[name], diags)
	}
}

// moduleProviderWarning is the one diagnostic [walkModuleProviders] raises
// per in-module provider block, naming the module, the block itself (aliased
// or default), and the root-serving truth live mode actually follows.
func moduleProviderWarning(modPath addrs.Module, pc *configs.Provider) *hcl.Diagnostic {
	block := fmt.Sprintf("provider %q", pc.Name)
	if pc.Alias != "" {
		block = fmt.Sprintf("provider %q, alias %q", pc.Name, pc.Alias)
	}
	return &hcl.Diagnostic{
		Severity: hcl.DiagWarning,
		Summary:  "Provider block inside a child module has no effect in live mode",
		Detail: fmt.Sprintf(
			"%s declares %s. Live mode does not consult a provider block declared inside a child module: this module's resources are served by the root configuration's own provider config, exactly as if this block were absent. Configure providers at root instead. See GitHub issue #70 for the full per-module provider resolution design.",
			modPath.String(), block,
		),
		Subject: pc.DeclRange.Ptr(),
	}
}
