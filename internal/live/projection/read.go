// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the read half of GitHub issue #187's fixpoint: the narrow
// live read that turns a handful of already-resolved managed resource
// instances into the values [identity.Context.ManagedResults] takes, so
// that a second resolution pass can answer a reference to a COMPUTED
// attribute of a sibling - aws_acm_certificate.cert.domain_validation_options
// - instead of refusing it as dynamic.
//
// It is deliberately not [Build]. A projection is the whole estate: it
// classifies orphans, applies ownership policy, hydrates records and
// produces the prior state a plan runs against. Running that twice is not
// merely expensive, it is unsound - see [ReadInstances]'s doc comment for
// the mechanism. What the fixpoint needs between the two resolution passes
// is much smaller: read these named instances, hand back their values, write
// nothing and classify nothing.

// ReadValues is the values a narrow read produced, keyed by absolute
// resource instance address exactly as [identity.Context.ManagedResults]
// expects them, together with the record of what could not be read.
type ReadValues struct {
	// Values is one whole live object per instance that was read, keyed by
	// [addrs.AbsResourceInstance.String]. It is the map to hand to
	// [identity.Context.ManagedResults] unaltered.
	//
	// Sensitivity marks the provider's schema declares ride on these values,
	// exactly as they do on internal/live/dataread's results, because both
	// seams feed the same index and the same static evaluator. Nothing in
	// this file looks inside a value, so no accessor here can meet a marked
	// receiver; the marksafe guards that matter are the ones in
	// internal/live/identity, on the paths that iterate a result.
	Values map[string]cty.Value

	// Unread lists every demanded instance that produced no value, in
	// address order, with the same machine-readable [Reason] a projection
	// records. An instance here is not an error: resolution refuses a
	// reference to it exactly as it did before this phase existed, which is
	// the safe direction. It is reported rather than dropped so that a
	// caller can say why a second pass changed nothing.
	Unread []Omission
}

// ReadInstances reads the live object of each given resolution and returns
// the values, for [identity.Context.ManagedResults].
//
// It is the read half of the #187 fixpoint: resolve, read the few managed
// instances a refused reference demanded, resolve again with those values in
// hand. The caller supplies the demand - this function reads what it is
// given and nothing else - because narrowing is the whole safety property,
// not an optimisation:
//
// A full [Build] is preceded by full marker discovery, and discovery's
// classifyOrphans (internal/live/discovery/discovery.go) withholds a live
// marker-carrying resource from removal only when its resource block still
// has an unbound declared instance. A block whose for_each is exactly what
// the fixpoint exists to resolve contributes no declared instances on the
// first pass, so pending is empty for it and every live resource carrying
// that block's marker falls through to Removal. The first pass therefore
// must not be a full pass: it must not sweep, must not collect unclaimed
// resources, and must be scoped to the instances the second pass needs.
// Today that unsoundness is unreachable only because a resolution error is
// fatal in internal/command/live_plan.go before discovery runs; a fixpoint
// that makes the first pass non-fatal is what would reach it.
//
// The demand carries one obligation this function cannot check for the
// caller, and getting it wrong is silent: resolutions must hold EVERY
// instance of each resource block it names, not only the instance a refused
// reference mentioned. A block's value is an aggregate over all its
// instances, so a demand of one instance out of two produces a
// single-element object that looks complete here and expands a dependent
// for_each to one key. [dropIncompleteBlocks] enforces completeness against
// the demand; only the caller knows the block's real expansion.
//
// Nothing here writes. No state is returned, no record store is touched, no
// orphan is classified and no removal is ever proposed. opts is accepted in
// full so that a read goes through the same provider selection and the same
// [Options.Ownership] rule a projection uses - a caller planning against an
// estate should set Ownership here for the same reason it sets it on
// [BuildWith], since an unowned live object's attributes would otherwise
// name the children of a resource this estate does not own.
//
// Termination is [orderWork]'s: parent-derived instances are read in
// dependency order and a cycle among them is reported rather than looped on.
// This function itself never iterates; the outer fixpoint's own termination
// is the caller's obligation.
func ReadInstances(ctx context.Context, cfg *configs.Config, resolutions []identity.Resolution, provs Providers, opts Options) (*ReadValues, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	empty := &ReadValues{Values: map[string]cty.Value{}}

	switch {
	case cfg == nil || cfg.Module == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to project",
			"Reading live objects for identity resolution requires the configuration the resolutions were computed from, and none was given.",
		))
		return empty, diags
	case provs == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider access",
			"Reading live objects for identity resolution requires configured provider instances to read the live system with, and none were given.",
		))
		return empty, diags
	}
	if len(resolutions) == 0 {
		return empty, diags
	}

	b := newBuilder(ctx, cfg, provs, opts)

	concrete, derived, needsDiscovery, cyclic, recordBacked := orderWork(resolutions)

	// A resolution whose identity this run does not hold cannot be read, and
	// saying so is the point: the second pass will refuse the reference that
	// demanded it, and a caller comparing the two passes needs to know which
	// of the two happened.
	for _, r := range needsDiscovery {
		b.omit(r.Addr, ReasonNeedsDiscovery, needsDiscoveryDetail(r), needsDiscoveryCause(r))
	}
	for _, r := range cyclic {
		b.omit(r.Addr, ReasonCycle, fmt.Sprintf(
			"The identities of %s and the instances it derives from refer to each other in a cycle, so there is no order in which to read them.", r.Addr,
		), "its identity formula is part of a cycle and can never be rendered.")
	}
	// A record-backed instance (issue #73) has no cloud object at all: its
	// prior state comes from the record store, which this phase deliberately
	// does not open. Reading one would mean deciding what a half-read record
	// means to an identity, which is a separate question from this one.
	for _, r := range recordBacked {
		b.omit(r.Addr, ReasonUnreadable, fmt.Sprintf(
			"%s is a record-backed effect, whose prior state lives in this estate's record store rather than in the cloud, so there is no live object to read its attributes from.", r.Addr,
		), "it is record-backed and has no live object to read.")
	}

	for _, r := range concrete {
		b.materialize(ctx, wanted{
			addr:       r.Addr,
			importID:   r.ImportID,
			identity:   r.Identity,
			values:     r.IdentityValues,
			undeclared: r.Undeclared,
		})
	}
	for _, r := range derived {
		id, values, ok := b.renderFormula(r)
		if !ok {
			continue
		}
		b.materialize(ctx, wanted{
			addr:       r.Addr,
			importID:   id,
			identity:   r.Identity,
			values:     values,
			undeclared: r.Undeclared,
		})
	}

	out := &ReadValues{Values: b.live}
	dropIncompleteBlocks(out, resolutions, b)

	sortOmissions(b.omissionList)
	out.Unread = b.omissionList
	return out, diags.Append(b.diags)
}

// dropIncompleteBlocks removes the values of any resource block whose
// demanded instances were not all read.
//
// [identity.Context.ManagedResults] is regrouped per resource block before
// the static evaluator sees it: a count block becomes a tuple in index
// order, a for_each block an object keyed by string. A block missing one of
// its instances has no honest aggregate, and handing one over is a caller
// error the identity package reports as such. More to the point, an
// aggregate assembled from three of a block's four instances is a WRONG
// answer rather than a missing one - a for_each over it would expand to
// three keys and the fourth resource would be planned as a create beside a
// live object nobody named.
//
// So a block is all-or-nothing, and the instances that were read are
// re-recorded as unread with the reason the block failed on, so the drop is
// visible rather than silent.
func dropIncompleteBlocks(out *ReadValues, demanded []identity.Resolution, b *builder) {
	// Every demanded instance, grouped by the resource block it belongs to.
	byBlock := make(map[string][]addrs.AbsResourceInstance, len(demanded))
	for _, r := range demanded {
		key := r.Addr.Module.String() + "|" + r.Addr.Resource.Resource.String()
		byBlock[key] = append(byBlock[key], r.Addr)
	}

	blocks := make([]string, 0, len(byBlock))
	for key := range byBlock {
		blocks = append(blocks, key)
	}
	sort.Strings(blocks)

	for _, key := range blocks {
		addrsInBlock := byBlock[key]
		var missing []addrs.AbsResourceInstance
		for _, addr := range addrsInBlock {
			if _, ok := out.Values[addr.String()]; !ok {
				missing = append(missing, addr)
			}
		}
		if len(missing) == 0 {
			continue
		}
		for _, addr := range addrsInBlock {
			if _, ok := out.Values[addr.String()]; !ok {
				continue
			}
			delete(out.Values, addr.String())
			b.omit(addr, ReasonIncompleteBlock, fmt.Sprintf(
				"%s was read, but %s of the same resource block could not be, and a resource whose instances are only partly known has no aggregate value a for_each or an identity argument could be resolved against. The whole block is left unread.",
				addr, missing[0],
			), "another instance of its resource block could not be read.")
		}
	}
}
