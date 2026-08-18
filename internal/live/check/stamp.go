// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"

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

// declaredEstateNames is [discovery.DeclaredEstateNames]. It used to be a
// body-for-body copy of internal/command's own, because internal/command
// imports this package and the reverse import would cycle - and neither
// copy had anything watching it. The shared implementation lives in
// internal/live/discovery, which both this package and internal/command
// already import for the marker tag keys themselves. Issue #285.
func declaredEstateNames(ctx context.Context, cfg *configs.Config) []string {
	return discovery.DeclaredEstateNames(ctx, cfg)
}

// stampNeedsDiscovery builds [stamp.Request.NeedsDiscovery] from identity
// resolution's own classification. The keying and the block-level fold both
// live in [identity.Result.DiscoveryCausesByBlock] now - this instrument and
// internal/command kept two copies of them, which is exactly the shape GitHub
// issue #111 was, so there is one.
//
// It says nothing about provider schemas, deliberately. GitHub issue #230's
// invariant - a type whose own schema this run could not read is UNKNOWN,
// never refused - lives in [stamp.SkipReason.Unknown] and is applied by
// stamp.Stamp itself, so every caller gets it: this instrument, "choudoufu
// live-plan", and anything that folds a [stamp.Result] into diagnostics of
// its own. The first fix for #230 filtered this map by schema presence
// instead, which held here and nowhere else, and it compared a different
// predicate from the one stamp applies (a key present in the map, versus a
// non-nil schema Block), so an entry carrying a schema with no block would
// have slipped past the gate straight into the refusal it was meant to
// prevent.
func stampNeedsDiscovery(result *identity.Result) map[string]identity.BlockDiscovery {
	return result.DiscoveryCausesByBlock()
}
