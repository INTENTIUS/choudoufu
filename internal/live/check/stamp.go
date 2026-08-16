// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// flatSchemas adapts [Context.Schemas] - a flat map keyed by resource type
// name, with any type two providers both serve already dropped (see
// internal/command's statelessProviders.resourceSchemas) - to [stamp.Schemas]
// and [projection.Schemas], which both ask for a provider and a resource
// mode alongside the type name.
//
// The two arguments are ignored rather than checked: Context.Schemas has
// already resolved provider ambiguity away by the time this package sees it,
// and every resource [stamp.Stamp] asks about is a managed resource by
// construction ([moduleResources] in that package walks mod.ManagedResources
// only), so a lookup by type name alone answers exactly what a
// provider-and-mode-aware lookup would.
type flatSchemas map[string]providers.Schema

func (s flatSchemas) ResourceTypeConfig(_ addrs.Provider, _ addrs.ResourceMode, resourceType string) (*providers.Schema, uint64) {
	schema, ok := s[resourceType]
	if !ok {
		return nil, 0
	}
	return &schema, 0
}

// syntheticStampEstate is the estate name [estateForStamp] writes when the
// configuration itself declares none. It satisfies [discovery.ValidEstateName]
// so [stamp.Stamp] runs past its own "no estate name" guard, and it is
// chosen to be unmistakably not a name a real deployment would already carry.
//
// Its value never changes what a stamp finding means. [stamp.Stamp]'s
// severity-bearing refusals - "Ownership markers not stamped" (a resource
// that could not be given a marker) and "Unmarked apply of a marker-only
// resource" (the same thing on an instance nothing but its marker can ever
// find again) - come from whether a resource's type is taggable and whether
// its identity is server-assigned, neither of which reads req.Estate at all
// (see stamper.mustStamp and stamper.unstampableAt). The one refusal that DOES
// compare against req.Estate's actual value, "Ownership marker conflict", only
// fires when the configuration already hardcodes a *different* literal
// tofu-estate value for the same resource - and when that is true,
// [declaredEstateNames] finds it and [estateForStamp] uses it instead of this
// constant, specifically so a synthetic name can never manufacture a conflict
// against the configuration's own declared value.
const syntheticStampEstate = "check-instrument"

// estateForStamp resolves the tofu-estate value to stamp with, for a
// configuration this offline instrument has no -estate flag or live block to
// read one from.
//
// This mirrors what a real "choudoufu live-plan" with no -estate flag does
// (internal/command/live_plan.go's statelessEstateFor): read the tofu-estate
// values the configuration's own tags arguments already hardcode. When
// exactly one is found, using it is not a choice this instrument is making -
// it is the same value a real run derives from the same configuration by the
// same rule, so a marker-conflict refusal built from it means exactly what it
// would in a real run.
//
// When the configuration declares none (every fixture in the 26-estate
// "clean" corpus, as of #224 - the tag is this fork's own convention, not one
// an onboarded configuration already has reason to carry) or declares more
// than one (a real run would refuse to guess and ask for -estate=<name>
// instead, a UX concern this offline check has no command line to raise), a
// syntactic placeholder is used. See [syntheticStampEstate] for why that
// placeholder cannot change what the resulting findings mean: the refusals
// stamp.Stamp can raise from a chosen name rather than a checked one is
// exactly the one case (a hardcoded, DIFFERENT literal value) declaredEstateNames
// already excludes by construction in the single-value case, and the
// zero/many cases have no declared value to conflict with at all.
func estateForStamp(ctx context.Context, cfg *configs.Config) string {
	declared := declaredEstateNames(ctx, cfg)
	if len(declared) == 1 && discovery.ValidEstateName(declared[0]) {
		return declared[0]
	}
	return syntheticStampEstate
}

// declaredEstateNames is [internal/command.statelessEstateFromConfig]
// reimplemented here rather than imported: internal/command already imports
// internal/live/check (to share this package's analysis with
// "choudoufu live-check"), so the reverse import would cycle. It walks the
// same tree the same way - every managed resource's own tags argument, in
// every module, read only when it is a literal object expression the static
// evaluator can settle - and returns the distinct literal tofu-estate values
// found, sorted.
func declaredEstateNames(ctx context.Context, cfg *configs.Config) []string {
	seen := make(map[string]bool)
	declaredEstateNamesFrom(ctx, cfg, seen)
	out := make([]string, 0, len(seen))
	for s := range seen {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func declaredEstateNamesFrom(ctx context.Context, cfg *configs.Config, seen map[string]bool) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	mod := cfg.Module
	if mod.StaticEvaluator == nil {
		return
	}

	names := make([]string, 0, len(mod.ManagedResources))
	for name := range mod.ManagedResources {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		rc := mod.ManagedResources[name]

		content, _, contentDiags := rc.Config.PartialContent(&hcl.BodySchema{
			Attributes: []hcl.AttributeSchema{{Name: "tags"}},
		})
		if contentDiags.HasErrors() {
			continue
		}
		attr, ok := content.Attributes["tags"]
		if !ok {
			continue
		}
		pairs, pairDiags := hcl.ExprMap(attr.Expr)
		if pairDiags.HasErrors() {
			// A tags argument that is not written as an object literal - a
			// merge() call, a variable - cannot be picked apart here.
			continue
		}
		for _, pair := range pairs {
			key, keyDiags := pair.Key.Value(nil)
			if keyDiags.HasErrors() || key.IsNull() || key.Type() != cty.String || key.AsString() != discovery.TagEstate {
				continue
			}
			val, valDiags := mod.StaticEvaluator.Evaluate(ctx, pair.Value, configs.StaticIdentifier{
				Module:    cfg.Path,
				Subject:   fmt.Sprintf("%s.tags", rc.Addr()),
				DeclRange: attr.Range,
			})
			if valDiags.HasErrors() || val.IsNull() || !val.IsWhollyKnown() || val.IsMarked() || val.Type() != cty.String {
				continue
			}
			if s := val.AsString(); s != "" {
				seen[s] = true
			}
		}
	}

	for _, name := range identity.SortedChildNames(cfg.Children) {
		declaredEstateNamesFrom(ctx, cfg.Children[name], seen)
	}
}

// stampNeedsDiscovery is [internal/command.statelessNeedsDiscovery]
// reimplemented for the same reason [declaredEstateNames] is: it builds
// [stamp.Request.NeedsDiscovery] from identity resolution's own
// classification, keyed the way stamp.mustStamp and stamp.Skip.Addr both key
// their lookups - by [addrs.ConfigResource.String], not by the keyed
// [addrs.AbsResourceInstance] resolution walks, so a resource inside a
// for_each'd module still matches (see GitHub issue #111).
//
// schemas gates the result per resource TYPE, not per run (GitHub issue
// #230): a hand-table-admitted type's ClassNeedsDiscovery classification is a
// static, generator-baked property of table_generated.go, independent of
// whether that type's own provider schema happened to load - schemas are
// only a fallback for a type the table does not cover. [stamp.Stamp] cannot
// tell the two "no schema" causes apart on its own (SkipNoSchema fires
// identically for a provider that failed entirely and one that loaded fine
// but does not model this particular type), so mustStamp - the thing that
// turns SkipNoSchema from a silent skip into a hard "unstampable" error -
// must never read true for a type this run has no schema for at all. A
// resource is therefore only counted as needing discovery here when its own
// type's schema is present in schemas; the previous gate instead asked
// whether ANY provider's schema had loaded anywhere in the configuration,
// which fabricated a hard refusal on every needs-discovery AWS resource the
// moment some unrelated provider (random, say) resolved while AWS's schema
// acquisition failed. Unknown must not be reported as refused.
func stampNeedsDiscovery(result *identity.Result, schemas map[string]providers.Schema) map[string]bool {
	if result == nil {
		return nil
	}
	needs := result.NeedsDiscovery()
	out := make(map[string]bool, len(needs))
	for _, r := range needs {
		if _, ok := schemas[r.Addr.Resource.Resource.Type]; !ok {
			continue
		}
		out[r.Addr.ContainingResource().Config().String()] = true
	}
	return out
}
