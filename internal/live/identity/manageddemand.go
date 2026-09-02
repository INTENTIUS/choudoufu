// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"sort"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the demand half of GitHub issue #187's fixpoint, the question
// [projection.ReadInstances] is the answer to: given a resolution pass that
// refused, WHICH managed resource blocks would a live read have to cover for
// a second pass to get further?
//
// internal/live/dataread answers the same question for data sources, and
// answers it by probing: it re-runs resolution with placeholder coverage and
// reads the [configs.RefusedReference] that [configs.StaticEvaluator] hangs
// on every reference it declined. That works because a data-source reference
// always reaches the static evaluator.
//
// A managed reference usually does not. [resolver.isSymbolic] recognises one
// BEFORE evaluation and routes it down the resource-expansion path, which
// raises its own diagnostics and never consults the static evaluator at all.
// Measured on internal/live/identity/testdata/managed-read-foreach: the one
// error a first pass produces is "Non-static for_each expression" carrying no
// Extra whatsoever, so a demand analysis written in dataread's shape would
// find nothing at all. The refusal sites therefore have to say what they
// wanted, which is what [resolver.refusedManaged] does, and this file reads
// back.

// ManagedDemand is one managed resource block a resolution pass could not get
// past, together with the instances of it that pass DID resolve.
//
// It is what a caller hands [projection.ReadInstances] - Instances is already
// the []Resolution that function takes - and Complete is the obligation that
// function's doc comment says only the caller can discharge: a demand of some
// of a block's instances produces an aggregate that looks whole and expands a
// dependent for_each to the wrong key set. A caller must read a demand only
// when Complete is true.
type ManagedDemand struct {
	// Module is the module path declaring the demanded block, unkeyed,
	// exactly as [configs.RefusedReference.Module] carries it.
	Module addrs.Module

	// Resource is the module-relative managed resource block.
	Resource addrs.Resource

	// NeededBy names what could not get past it: the identity-bearing
	// subject whose resolution raised the refusal, rendered the way the
	// diagnostic's own text renders it.
	NeededBy string

	// Instances is every instance of this block the pass resolved, in
	// address order. Empty when the block itself did not resolve.
	Instances []Resolution

	// Complete reports whether Instances is every instance of the block
	// there is. It is false when the pass resolved none of them, and false
	// when the pass raised a diagnostic against an instance of this very
	// block - which is the case where some instances resolved and others did
	// not, and is the case a caller must not read.
	//
	// A false here is not an error. It says the fixpoint has nothing to gain
	// from this demand, because a partial answer is a WRONG answer rather
	// than a smaller one.
	Complete bool
}

// DemandedManagedReads derives the managed resource blocks a resolution pass
// needs the live values of, from that pass's own result and diagnostics.
//
// result may be nil - a pass that failed outright still names its demand -
// in which case every entry comes back with no instances and Complete false.
// The order is stable: module path, then block address.
//
// This function performs no read and consults no provider. It is offline in
// the same sense internal/live/dataread's own analysis is, and a caller with
// no cloud can use it to say WHY a second pass would change nothing.
func DemandedManagedReads(result *Result, diags tfdiags.Diagnostics) []ManagedDemand {
	type key struct {
		mod string
		res string
	}
	order := make([]key, 0, len(diags))
	byKey := make(map[key]*ManagedDemand, len(diags))

	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		ref := tfdiags.ExtraInfo[configs.RefusedReference](d)
		if ref.Category != configs.CategoryManagedResource {
			continue
		}
		res, ok := managedSubject(ref.Subject)
		if !ok {
			continue
		}
		k := key{mod: ref.Module.String(), res: res.String()}
		if _, seen := byKey[k]; seen {
			continue
		}
		byKey[k] = &ManagedDemand{Module: ref.Module, Resource: res, NeededBy: ref.NeededBy}
		order = append(order, k)
	}

	sort.Slice(order, func(i, j int) bool {
		if order[i].mod != order[j].mod {
			return order[i].mod < order[j].mod
		}
		return order[i].res < order[j].res
	})

	out := make([]ManagedDemand, 0, len(order))
	for _, k := range order {
		d := byKey[k]
		d.Instances = instancesOfBlock(result, d.Module, d.Resource)
		d.Complete = len(d.Instances) > 0 && !blockRaisedADiagnostic(diags, d.Module, d.Resource)
		out = append(out, *d)
	}
	return out
}

// managedSubject extracts the containing managed resource from a reference's
// subject, when it names one. It is [dataread.DataSubject]'s counterpart for
// the other resource mode, kept here because this package is where the
// managed refusals are raised.
func managedSubject(subject addrs.Referenceable) (addrs.Resource, bool) {
	switch s := subject.(type) {
	case addrs.Resource:
		if s.Mode == addrs.ManagedResourceMode {
			return s, true
		}
	case addrs.ResourceInstance:
		if s.Resource.Mode == addrs.ManagedResourceMode {
			return s.ContainingResource(), true
		}
	}
	return addrs.Resource{}, false
}

// instancesOfBlock selects every resolution belonging to one block, across
// every INSTANCE of the declaring module. The demand names an unkeyed module
// path because a static evaluator is shared by every instance of its module;
// the resolutions are keyed, and all of them are read, because a block inside
// a for_each'd module has one set of live objects per module instance and an
// aggregate assembled from one of them would be the partial answer this
// mechanism exists to refuse.
func instancesOfBlock(result *Result, mod addrs.Module, res addrs.Resource) []Resolution {
	if result == nil {
		return nil
	}
	var out []Resolution
	for _, r := range result.All() {
		if !r.Addr.Module.Module().Equal(mod) {
			continue
		}
		if r.Addr.Resource.Resource != res {
			continue
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.String() < out[j].Addr.String() })
	return out
}

// blockRaisedADiagnostic reports whether any error in diags is tagged with an
// [InstanceFailure] naming an instance of this block. That tag is what says
// "this instance did not resolve", and a block with both resolved and
// unresolved instances has no honest aggregate - see [ManagedDemand.Complete].
func blockRaisedADiagnostic(diags tfdiags.Diagnostics, mod addrs.Module, res addrs.Resource) bool {
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		fail := tfdiags.ExtraInfo[InstanceFailure](d)
		if fail.Addr == "" {
			continue
		}
		addr, addrDiags := addrs.ParseAbsResourceInstanceStr(fail.Addr)
		if addrDiags.HasErrors() {
			continue
		}
		if addr.Module.Module().Equal(mod) && addr.Resource.Resource == res {
			return true
		}
	}
	return false
}

// demandedManaged records, on the diagnostic [resolver.errorf] appended
// immediately before it, that a live read of subject is what would settle the
// refusal. Nothing else about the diagnostic changes - severity, summary,
// detail, range and the [InstanceFailure] tag are all exactly what errorf
// produced - so nothing downstream that counts or renders refusals can tell a
// tagged one from an untagged one.
//
// It is deliberately a second call rather than an errorf variant taking the
// summary as a parameter. internal/live/refusalscan reads summaries
// statically and fails the build on a summary it cannot see as a literal at
// the raise site, which is what keeps [AllRefusals] honest; routing one
// through a wrapper's string parameter would have hidden it.
//
// subject is the reference's own subject, module-relative. A subject that is
// not a managed resource is recorded not at all rather than emptily, so a
// reader cannot mistake "nothing was demanded" for "a demand with no
// subject".
func (r *resolver) demandedManaged(subject addrs.Referenceable, neededBy string) {
	if _, ok := managedSubject(subject); !ok {
		return
	}
	if len(r.diags) == 0 {
		return
	}
	ref := configs.RefusedReference{
		Category: configs.CategoryManagedResource,
		Subject:  subject,
		Module:   r.modInst.Module(),
		NeededBy: neededBy,
	}
	last := r.diags[len(r.diags)-1]
	// The same two-link chain [resolver.appendDiags] builds for a diagnostic
	// that already carried an Extra: the [InstanceFailure] tag every consumer
	// of this package reads sits in front and unwraps to the reference, so
	// tfdiags.ExtraInfo finds either one.
	var extra interface{} = ref
	if fail := tfdiags.ExtraInfo[InstanceFailure](last); fail.Addr != "" {
		extra = InstanceFailure{Addr: fail.Addr, inner: ref}
	}
	r.diags[len(r.diags)-1] = managedDemandDiag{Diagnostic: last, extra: extra}
}

// managedDemandDiag is one diagnostic with a different Extra chain and
// nothing else changed. See [resolver.demandedManaged].
type managedDemandDiag struct {
	tfdiags.Diagnostic
	extra interface{}
}

func (d managedDemandDiag) ExtraInfo() interface{} { return d.extra }
