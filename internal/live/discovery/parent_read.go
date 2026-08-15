// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The parent-read sweep leg (issue #60). live/LIMITATIONS.md's "Untaggable
// types carry no ownership marker of their own" entry is what this file
// closes part of: an untaggable admitted type cannot be found by the
// ordinary marker sweep in [sweepTypes] and [scanType], because a type with
// no tags argument has nowhere to write a tofu-estate tag. Several of them
// do not need one. A bucket policy's whole identity is the parent bucket's
// own name; reading the bucket - marked, admitted, found the ordinary way -
// tells this pass the policy's identity too, with no memory of the policy
// itself required. identity.SingleParentComponent is the mechanical test
// for that shape (see internal/live/identity/parent.go); this file is what
// acts on it.
//
// It is deliberately not another branch of scanType. That function is
// driven by [sweepTypes], whose universe already excludes any type with
// even one declared instance - correct for the ordinary sweep, because a
// declared, client-named instance resolves without discovery at all and
// scanning the type again would be redundant. It is wrong here: a bucket
// with a declared policy and a second bucket with none must be told apart,
// and that is a per-parent-instance question a per-type exclusion cannot
// answer. So this leg runs unconditionally over every parent-readable
// untaggable type, and decides "declared or not" per parent value instead
// of per type.
package discovery

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// parentReadSweep is the leg's entry point, called once per [Discover] pass
// that asked for a sweep. It costs one list call per (parent-readable
// untaggable type, bound parent instance not already declaring a child) -
// bounded by the estate's own size, the same way the ordinary sweep is
// bounded by the admission table's.
func parentReadSweep(ctx context.Context, req Request, schemas listclient.Schemas, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	eligibleParents := taggableAdmittedTypes(schemas)

	for _, typeName := range identity.AdmittedTypes() {
		ts, ok := schemas.Get(typeName)
		if !ok || markerCapable(ts) {
			// Not listable at all - a different gap, [scanType] already
			// reports it - or taggable, meaning the ordinary marker sweep
			// already covers it and this leg has nothing to add.
			continue
		}
		link, ok := identity.SingleParentComponent(typeName, eligibleParents, rosterServiceOf(req.Roster))
		if !ok {
			// Either not parent-readable at all, or parent-readable in a
			// shape this pass does not yet act on (a second free-standing
			// argument the parent alone does not supply) - see
			// live/LIMITATIONS.md's parent-read table.
			continue
		}
		diags = diags.Append(parentReadSweepType(ctx, req, schemas, typeName, link, res))
	}
	return diags
}

// rosterServiceOf adapts the run's registry roster to [identity.ServiceOf],
// so the parent derivation can tell whether a candidate belongs to the
// child's own AWS service (issue #129). A run with no roster - Request.Roster
// is optional, see its doc comment - supplies nil, and the derivation falls
// back to Terraform-prefix affinity.
func rosterServiceOf(r *registry.Roster) identity.ServiceOf {
	if r == nil {
		return nil
	}
	return r.ServiceOf
}

// taggableAdmittedTypes is the eligible-parent set [identity.ParentOf] and
// [identity.SingleParentComponent] both need: every admitted type this
// provider's schema says can carry tags, computed live rather than from
// live/survey-full.json's committed signal, since a run has the provider in
// hand and tools/survey-gen does not. See internal/live/identity/parent.go
// for why an ineligible parent (an admitted type that cannot itself carry a
// marker) must never anchor a read.
func taggableAdmittedTypes(schemas listclient.Schemas) map[string]bool {
	out := make(map[string]bool)
	for _, typeName := range identity.AdmittedTypes() {
		if ts, ok := schemas.Get(typeName); ok && markerCapable(ts) {
			out[typeName] = true
		}
	}
	return out
}

// parentReadSweepType runs the leg for one parent-readable untaggable type:
// every bound parent instance this pass resolved, checked against the
// children already declared for that type, with a scoped read for the rest.
//
// Whether that read is one list call per undeclared parent or one list call
// for the whole type depends on the child's own list configuration. When it
// accepts the linking argument (hasAttr(ts.Config, link.Attr)), each list
// call is server-side scoped to one parent, so paying for one per undeclared
// parent is the right cost. When it does not, [listclient.List] has no
// filter to send at all - the provider itself must enumerate every parent
// to answer, an S3-bucket-sub-resource shape where "list" is really
// "describe every bucket" - and that unscoped list already returns every
// parent's child in one call. Issuing it once per undeclared parent instead
// of once per type made this leg accidentally quadratic in the estate's own
// size (confirmed against TestPlanCallBudgetAgainstFloci: N=200 measured
// ~40200 calls on four of these types, 200 unscoped lists each paying
// O(200) - not the O(200) total the budget expects).
func parentReadSweepType(ctx context.Context, req Request, schemas listclient.Schemas, typeName string, link identity.ParentLink, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	ts, ok := schemas.Get(typeName)
	if !ok {
		return diags
	}
	scoped := hasAttr(ts.Config, link.Attr)

	// The child values this type already has a declared, composed
	// resolution for - a client-named resource block resolves concrete
	// without discovery at all, so any declared instance of a
	// parent-readable type is already sitting in res.Resolutions with its
	// composed identity by the time this leg runs. See the package doc for
	// why this is checked per value rather than by excluding the whole
	// type the way [sweepTypes] does.
	declared := make(map[string]bool)
	for _, r := range res.Resolutions {
		if r.Type() != typeName || r.Class != identity.ClassConcrete {
			continue
		}
		declared[r.ImportID] = true
	}

	// byParentValue is only built and consulted in the unscoped case: one
	// list call for the whole type, indexed by the identity value that
	// pins a result to its parent, so every undeclared parent below is a
	// map lookup rather than a fresh call.
	var byParentValue map[string]listclient.Result
	if !scoped {
		var listDiags tfdiags.Diagnostics
		byParentValue, listDiags = listUnscopedChildren(ctx, req, ts, typeName)
		if listDiags.HasErrors() {
			// Same restraint as the per-parent path below: a provider
			// hiccup on a type nothing in the configuration mentions must
			// not fail the whole run.
			return diags
		}
		diags = diags.Append(listDiags)
	}

	for _, r := range res.Resolutions {
		if r.Type() != link.Parent || r.Class != identity.ClassConcrete || r.ImportID == "" {
			continue
		}
		if modCfg, ok := identity.ConfigForModule(req.Config, r.Addr.Module); ok && modCfg.Module != nil {
			if rc, ok := modCfg.Module.ManagedResources[r.Addr.Resource.Resource.String()]; ok && !inScope(req.ScopeProvider, rc, r.Addr.Module.Module()) {
				// Issue #69's multi-provider sweep: this parent belongs to a
				// different provider configuration, which is the pass
				// actually responsible for reading its children. Reading it
				// here too would be a call against the wrong account at
				// best, and at worst a second, duplicate [ParentReadFinding]
				// and synthetic resolution once every pass's results are
				// merged.
				continue
			}
		}
		parentValue := r.ImportID
		if declared[parentValue] {
			continue
		}
		if scoped {
			diags = diags.Append(readParentChildScoped(ctx, req, ts, typeName, link, r.Addr, parentValue, res))
			continue
		}
		if found, ok := byParentValue[parentValue]; ok {
			recordParentReadFinding(typeName, link, r.Addr, parentValue, found, res)
		}
	}
	return diags
}

// readParentChildScoped reads one (child type, parent instance) pair with a
// list call scoped server-side to parentValue - the cheap path, used only
// when the child's own list configuration accepts the linking argument. A
// result is only ever accepted when its own identity equals parentValue
// exactly - the whole of what a named-singleton child's identity is - so a
// provider that ignores the scope can never misattribute one parent's child
// to another.
func readParentChildScoped(ctx context.Context, req Request, ts listclient.TypeSchema, typeName string, link identity.ParentLink, parentAddr addrs.AbsResourceInstance, parentValue string, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	vals := map[string]cty.Value{link.Attr: cty.StringVal(parentValue)}
	if hasAttr(ts.Config, "region") && req.Region != "" {
		vals["region"] = cty.StringVal(req.Region)
	}
	config, cfgDiags := ts.BuildConfig(vals)
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		return diags
	}

	results, listDiags := listclient.List(ctx, req.Provider, typeName, config, true)
	if listDiags.HasErrors() {
		// Not appended to diags, the same restraint [scanType] shows for a
		// sweep type's list failure: a provider hiccup on a type nothing in
		// the configuration mentions must not fail the whole run. There is
		// no SweepGap-style ledger entry for this leg yet; it is silent
		// rather than misreported, which a follow-on pass can improve on.
		return diags
	}
	diags = diags.Append(listDiags)

	for _, r := range results {
		importID, _, hasID := importIdentity(typeName, r)
		if !hasID || importID != parentValue {
			continue
		}
		recordParentReadFinding(typeName, link, parentAddr, parentValue, r, res)
		// A named-singleton child has at most one live instance per parent;
		// nothing more to look for once one is found.
		break
	}
	return diags
}

// listUnscopedChildren runs the one list call a type whose list
// configuration cannot be scoped to a parent gets for the whole
// [parentReadSweepType] pass, indexed by each result's own import identity.
// A later import ID collision (two results resolving to the same identity,
// which a well-formed named-singleton child's provider should never
// produce) keeps the first result seen, the same "first match wins"
// discipline [readParentChildScoped]'s break already applies per parent.
func listUnscopedChildren(ctx context.Context, req Request, ts listclient.TypeSchema, typeName string) (map[string]listclient.Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	vals := make(map[string]cty.Value)
	if hasAttr(ts.Config, "region") && req.Region != "" {
		vals["region"] = cty.StringVal(req.Region)
	}
	config, cfgDiags := ts.BuildConfig(vals)
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		return nil, diags
	}

	results, listDiags := listclient.List(ctx, req.Provider, typeName, config, true)
	diags = diags.Append(listDiags)
	if listDiags.HasErrors() {
		return nil, diags
	}

	byParentValue := make(map[string]listclient.Result, len(results))
	for _, r := range results {
		importID, _, hasID := importIdentity(typeName, r)
		if !hasID {
			continue
		}
		if _, exists := byParentValue[importID]; !exists {
			byParentValue[importID] = r
		}
	}
	return byParentValue, diags
}

// recordParentReadFinding is the one place a matched (child type, parent
// instance) pair turns into a [ParentReadFinding] and, for a removable
// type, an undeclared [identity.Resolution] - shared by both
// [readParentChildScoped]'s per-parent match and [parentReadSweepType]'s
// unscoped map lookup, so the two list strategies above stay
// indistinguishable to everything downstream of a match.
func recordParentReadFinding(typeName string, link identity.ParentLink, parentAddr addrs.AbsResourceInstance, parentValue string, r listclient.Result, res *Result) {
	importID, idAttr, hasID := importIdentity(typeName, r)
	if !hasID {
		return
	}

	finding := ParentReadFinding{
		TypeName:     typeName,
		Parent:       link.Parent,
		ParentAddr:   parentAddr,
		ParentValue:  parentValue,
		ImportID:     importID,
		IdentityAttr: idAttr,
		Identity:     r.Identity,
		DisplayName:  r.DisplayName,
	}

	if identity.ParentReadRemovable(typeName) {
		finding.Removal = true
		addr := syntheticChildAddr(typeName, parentAddr)
		res.Resolutions = append(res.Resolutions, identity.Resolution{
			Addr:       addr,
			Class:      identity.ClassConcrete,
			ImportID:   importID,
			Identity:   r.Identity,
			Undeclared: true,
		})
	} else {
		finding.Withheld = fmt.Sprintf(
			"%s is parent-readable via %s but not yet wired for removal by this pass; see live/LIMITATIONS.md, \"Some are swept via a parent read instead (issue #60)\"",
			typeName, link.Parent)
	}
	res.ParentReads = append(res.ParentReads, finding)
}

// syntheticChildAddr is the address a removable parent-read finding enters
// the prior state at. A swept orphan's address comes from unescaping its
// own tofu-address marker - the block's original name, recorded when it was
// first applied - and a parent-read finding has no such marker to read: the
// child never carried one. What it has instead is the parent's own resolved
// address, and the convention every fixture in this tree already follows
// for a named-singleton child (aws_s3_bucket.data alongside
// aws_s3_bucket_policy.data) is to give the child block the parent's own
// resource name. Reusing it here is a best-effort label, not a recovered
// fact: if the child's original block used a different name, the destroy
// still targets the right live resource (ImportID and Identity came from
// the read, not from this address), only the printed address may not match
// history. It cannot collide with a currently-declared instance of the same
// type and name, because [parentReadSweepType] only reaches here when no
// declared resolution of typeName claims parentValue, and two configuration
// blocks of one type can never share one name.
func syntheticChildAddr(childType string, parentAddr addrs.AbsResourceInstance) addrs.AbsResourceInstance {
	return addrs.AbsResourceInstance{
		Module: parentAddr.Module,
		Resource: addrs.ResourceInstance{
			Resource: addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: childType,
				Name: parentAddr.Resource.Resource.Name,
			},
			Key: parentAddr.Resource.Key,
		},
	}
}
