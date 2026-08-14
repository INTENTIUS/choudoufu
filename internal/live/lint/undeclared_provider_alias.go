// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// checkUndeclaredProviderAlias rejects a root-module resource whose provider
// argument names an alias the root module does not declare.
//
// GitHub issue #123, the second route the phase-4 audit found into
// internal/command/live_plan.go's providerConfigValue fallback (#104's
// module-mapping rule covers the first). Stock OpenTofu refuses this
// configuration in the graph: ProviderTransformer errors with "Provider
// configuration not present". Live mode resolves the address much earlier,
// during marker discovery, and the lookup miss used to fall through to
// hcl.EmptyBody() - the provider was configured from the environment alone,
// with nothing from the configuration reaching it and no diagnostic saying
// so. Established by running it (the fixture
// internal/command/testdata/live-plan-undeclared-provider-alias): discovery
// had already scanned types through other providers before the stray address
// was even looked up, and the real AWS provider accepts an empty
// configuration, so against a real account the run simply proceeds against
// whatever account and region the environment happens to name.
//
// Root module only. A child-module resource naming an alias resolves through
// configuration_aliases and the call's providers mapping, which upstream
// config validation and [checkModuleProviderMapping] already police between
// them; recursing here would double-report those.
//
// A resource with no provider argument, or one naming an unaliased provider,
// is not this rule's business: an absent root provider block for the default
// configuration is the documented way a provider takes everything from the
// environment, and refusing it would refuse configurations that work today.
func checkUndeclaredProviderAlias(mod *configs.Module, path addrs.Module, issues *[]Issue) {
	if !path.IsRoot() {
		return
	}

	names := make([]string, 0, len(mod.ManagedResources))
	for name := range mod.ManagedResources {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		resource := mod.ManagedResources[name]
		ref := resource.ProviderConfigRef
		if ref == nil || ref.Alias == "" {
			continue
		}
		key := ref.Name + "." + ref.Alias
		if mod.ProviderConfigs[key] != nil {
			continue
		}

		subject := ref.NameRange
		if ref.AliasRange != nil {
			subject = *ref.AliasRange
		}

		*issues = append(*issues, Issue{
			Rule:      RuleUndeclaredProviderAlias,
			Construct: fmt.Sprintf("%s, provider = %s", resource.Addr(), key),
			Module:    path,
			Detail: fmt.Sprintf(
				"%s names the provider configuration %s, and the root module declares no such provider block. "+
					"Live mode would configure that provider from the environment alone - none of the "+
					"configuration's provider settings would reach it, and the resource would be read, written "+
					"and swept against whatever account and region the environment names. Declare "+
					"provider %q with alias = %q, or drop the resource's provider argument to use the default "+
					"configuration.",
				resource.Addr(), key, ref.Name, ref.Alias,
			),
			Subject: subject,
		})
	}
}
