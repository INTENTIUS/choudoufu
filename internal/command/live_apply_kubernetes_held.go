// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"log"
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/kubesweep"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// The command layer's half of GitHub issue #1184's post-apply check (the
// engine half, and what the check is, is discovery.HeldKubernetesDeletes).
//
// Two moments, because no one moment has everything. The plan is the only
// place a delete is written down, and tofu.Context.Apply drains it as it
// goes (issue #908), so the deletes are read in AfterPlan. Whether one of
// them was only accepted can be asked only once the apply has run, so the
// lists happen in AfterApply, through the sweep's own cluster clients -
// which are not provider plugins and so are still usable after PriorState
// closed those.

// kubernetesDeleteSet is what one provider configuration's cluster is asked
// about after the apply: the deletes the plan scheduled through it, and the
// type universe the sweep's kind join needs.
type kubernetesDeleteSet struct {
	types        []string
	manifestType string
	deleted      []discovery.DeletedKubernetesObject
}

// kubernetesTypeUniverse reads the Kubernetes sweep's universe off a
// provider's schema: every type of the object-metadata shape, and the one
// type of the manifest shape when the provider has one. By shape, never by
// name.
func kubernetesTypeUniverse(schema providers.ProviderSchema) (types []string, manifestType string) {
	for name, rs := range schema.ResourceTypes {
		if _, ok := identity.ObjectMetaShape(rs.Block); ok {
			types = append(types, name)
		}
		if identity.ManifestShape(rs.Block) {
			// GitHub issue #1079: the type the manifest shape admits,
			// found by shape and never by name, puts every served kind
			// in the sweep's universe, CRDs included.
			types = append(types, name)
			manifestType = name
		}
	}
	sort.Strings(types)
	return types, manifestType
}

// statelessKubernetesDeletes reads the plan's deletes that went through a
// provider configuration the sweep holds a cluster client for, keyed the
// way the clients are. Holding a client is what makes a provider
// configuration one whose objects are label-swept; every other provider's
// deletes, AWS's included, are not looked at, because "accepted, still
// listed, terminating" is a state only the Kubernetes API reports.
//
// A replace counts: its delete leg is a delete, and an object still there
// with a deletionTimestamp is the old one whichever leg ran first.
//
// identities is the run's address-keyed index of resolved identities
// ([projection.NewMarkerIndex]), which holds a declared block and a swept
// orphan alike, so a block removed from source and `apply -destroy` are
// named the same way. A delete it has no import id for is left out: it
// cannot be joined to a listed object, and a guess is worse than silence.
//
// Nil when the plan deletes nothing such, which is what keeps AfterApply
// from asking any cluster anything.
func statelessKubernetesDeletes(sweepers map[string]kubesweep.Sweeper, plan *plans.Plan, schemas *tofu.Schemas, identities map[string]providers.ImportTarget) map[string]*kubernetesDeleteSet {
	if plan == nil || plan.Changes == nil || schemas == nil || len(sweepers) == 0 {
		return nil
	}
	var out map[string]*kubernetesDeleteSet
	for _, rc := range plan.Changes.Resources {
		if rc == nil || rc.Addr.Resource.Resource.Mode != addrs.ManagedResourceMode {
			continue
		}
		if rc.Action != plans.Delete && !rc.Action.IsReplace() {
			continue
		}
		key := providerCacheKey(rc.ProviderAddr)
		if sweepers[key] == nil {
			continue
		}
		target, ok := identities[rc.Addr.String()]
		if !ok || target.ID == "" {
			continue
		}
		set := out[key]
		if set == nil {
			set = &kubernetesDeleteSet{}
			set.types, set.manifestType = kubernetesTypeUniverse(schemas.ProviderSchema(rc.ProviderAddr.Provider))
			if out == nil {
				out = map[string]*kubernetesDeleteSet{}
			}
			out[key] = set
		}
		set.deleted = append(set.deleted, discovery.DeletedKubernetesObject{
			Addr:     rc.Addr,
			TypeName: rc.Addr.Resource.Resource.Type,
			ImportID: target.ID,
		})
	}
	return out
}

// statelessHeldKubernetesDeletes asks each cluster about the deletes that
// went through it and returns the one warning, or nothing. A cluster that
// cannot answer is logged and not reported: this check is a courtesy over
// an apply that already succeeded, the next plan lists the same objects
// and says so loudly if it cannot, and a second warning here would be one
// about this check rather than about the estate.
func statelessHeldKubernetesDeletes(ctx context.Context, sweepers map[string]kubesweep.Sweeper, deletes map[string]*kubernetesDeleteSet, estate string) tfdiags.Diagnostics {
	if len(deletes) == 0 || estate == "" {
		return nil
	}
	keys := make([]string, 0, len(deletes))
	for key := range deletes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var held []discovery.HeldKubernetesDelete
	for _, key := range keys {
		set := deletes[key]
		found, err := discovery.HeldKubernetesDeletes(ctx, sweepers[key], set.types, set.manifestType, estate, set.deleted)
		if err != nil {
			log.Printf("[WARN] live: post-apply check for deletes the cluster only accepted could not list %s: %s", key, err)
		}
		held = append(held, found...)
	}
	return discovery.HeldKubernetesDeletesDiag(held)
}
