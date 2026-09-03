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
	"strings"

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
		if link, ok := identity.SingleParentComponent(typeName, eligibleParents, rosterServiceOf(req.Roster)); ok {
			diags = diags.Append(parentReadSweepType(ctx, req, schemas, typeName, link, res))
			continue
		}
		if link, ok := identity.ParentListRecovered(typeName, eligibleParents, rosterServiceOf(req.Roster)); ok {
			// The multi-component shape SingleParentComponent excludes: a
			// second, free-standing identity component the parent does not
			// supply, but a parent-scoped list returns for every live child
			// (issue #692's parent-keyed IAM orphan-recovery tail).
			diags = diags.Append(parentListChildSweepType(ctx, req, schemas, typeName, link, res))
			continue
		}
		// Either not parent-readable at all, or parent-readable in a shape
		// no leg acts on yet - see live/LIMITATIONS.md's parent-read table.
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

	// The parents this leg would actually read a child for, settled BEFORE
	// any call is made. Both paths below consult exactly this list, so an
	// empty one means neither path has anything to do and neither should
	// spend a call finding that out.
	//
	// The scoped path always had that property - its call is inside the
	// loop. The unscoped path did not: [listUnscopedChildren] ran first,
	// unconditionally, for every parent-readable untaggable type in the
	// whole admission table, whether or not this estate owns a single
	// object of the parent type. Measured on a migrated 79-instance
	// terralith that declares no bucket, no queue, no topic, no repository
	// and no secret, that was ten list calls - five ListBuckets, plus
	// ListQueues, ListTopics, ListSecrets, DescribeRepositories and one
	// more bucket-level GET - every one of them enumerating a service the
	// configuration does not mention, on every plan. The cost of this leg
	// is meant to be bounded by the estate ("one list call per
	// (parent-readable untaggable type, bound parent instance not already
	// declaring a child)", above); an unconditional list is bounded by the
	// admission table instead.
	//
	// Nothing about what the leg FINDS changes: byParentValue was only
	// ever read through this same filtered set, so a run with no candidate
	// parent could never have produced a finding from it.
	type parentCandidate struct {
		addr  addrs.AbsResourceInstance
		value string
	}
	var candidates []parentCandidate
	for _, r := range res.Resolutions {
		if r.Type() != link.Parent || r.Class != identity.ClassConcrete || r.ImportID == "" {
			continue
		}
		if modCfg, ok := identity.ConfigForModule(req.Config, r.Addr.Module); ok && modCfg.Module != nil {
			if rc, ok := modCfg.Module.ManagedResources[r.Addr.Resource.Resource.String()]; ok && !inScope(req.ScopeProvider, rc, modCfg) {
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
		if parentHeldByOtherEstate(res, link.Parent, r.ImportID) {
			continue
		}
		if declared[r.ImportID] {
			continue
		}
		candidates = append(candidates, parentCandidate{addr: r.Addr, value: r.ImportID})
	}
	if len(candidates) == 0 {
		return diags
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

	for _, c := range candidates {
		if scoped {
			diags = diags.Append(readParentChildScoped(ctx, req, ts, typeName, link, c.addr, c.value, res))
			continue
		}
		if found, ok := byParentValue[c.value]; ok {
			recordParentReadFinding(typeName, link, c.addr, c.value, found, res)
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

// parentHeldByOtherEstate reports whether the sweep saw the live object at
// (parentType, importID) carrying a tofu-estate tag naming a different
// estate than this run's. The 2026-09-03 ruling is that the live tag
// decides: a parent another estate holds never anchors a child read for
// this one, whatever a left-behind record says. A nil map or a missing
// entry reads false, which is the safe reading here BECAUSE the only
// candidates reaching this check are already ClassConcrete resolutions this
// estate's own configuration declares - a parent an operator asserts is
// theirs - so "the sweep never saw it held elsewhere" leaves that assertion
// standing rather than inventing a cross-estate move from an absent tag.
func parentHeldByOtherEstate(res *Result, parentType, importID string) bool {
	return res.OtherEstateHeld[parentType][importID]
}

// childImportID recovers a parent-list-recovered child's whole import
// identity from one list result. It first tries the ordinary single-attr
// read ([importIdentity]); when that finds nothing - a composite type whose
// provider identity schema carries its parts separately (role and name for
// aws_iam_role_policy) rather than a pre-composed id - it renders the import
// string from the identity table's own Components, reading each
// argument-supplying component's identity attribute and gluing the literals
// (the ":" between role and policy name) between them. The result equals
// what the resolver composes for the same instance from configuration, so a
// declared child of this parent is recognised and skipped.
func childImportID(typeName string, r listclient.Result) (string, bool) {
	if id, _, ok := importIdentity(typeName, r); ok {
		return id, true
	}
	ti, ok := identity.LookupType(typeName)
	if !ok || len(ti.Components) == 0 {
		return "", false
	}
	var b strings.Builder
	for _, c := range ti.Components {
		b.WriteString(c.Literal)
		if len(c.Attrs) == 0 {
			continue
		}
		got := false
		for _, argName := range c.Attrs {
			idAttr := c.IdentityAttr
			if idAttr == identity.SameNameIdentity {
				idAttr = argName
			}
			if idAttr == "" {
				continue
			}
			if v, ok := r.IdentityAttr(idAttr); ok && v != "" {
				b.WriteString(v)
				got = true
				break
			}
		}
		if !got {
			return "", false
		}
	}
	out := b.String()
	if out == "" {
		return "", false
	}
	return out, true
}

// listScopeAttr picks the child list configuration's argument that carries
// the parent value. It prefers the identity component's own name (link.Attr,
// what [readParentChildScoped]'s single-component path always uses), and
// falls back to the list resource's sole non-region attribute when the two
// differ: aws_iam_role_policy's identity component is "role" but its list
// resource scopes on "role_name", the same value under another name. An
// empty return means the list cannot be scoped to a parent at all.
func listScopeAttr(ts listclient.TypeSchema, linkAttr string) string {
	if hasAttr(ts.Config, linkAttr) {
		return linkAttr
	}
	if ts.Config == nil {
		return ""
	}
	var sole string
	for name := range ts.Config.Attributes {
		if name == "region" {
			continue
		}
		if sole != "" {
			// More than one candidate: too ambiguous to guess which is the
			// parent scope, so this leg does not act (report-only stays the
			// safe default for an unrecognised list shape).
			return ""
		}
		sole = name
	}
	return sole
}

// parentListChildSweepType runs the parent-list-recovered leg for one
// multi-component untaggable child (issue #692): every bound parent
// instance this pass resolved, its children enumerated by a parent-scoped
// list, and every child not already declared recorded as an orphan. Unlike
// [parentReadSweepType] it never skips a parent that declares SOME child of
// this type, because a role may declare one inline policy and own another
// it no longer declares - the declared/undeclared judgment is made per
// child identity, not per parent.
func parentListChildSweepType(ctx context.Context, req Request, schemas listclient.Schemas, typeName string, link identity.ParentLink, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	ts, ok := schemas.Get(typeName)
	if !ok {
		return diags
	}
	scopeAttr := listScopeAttr(ts, link.Attr)
	if scopeAttr == "" {
		// This shape recovers children only through a list scoped to one
		// parent. When the child's list configuration exposes no argument
		// to carry the parent value - the identity component's own name
		// (link.Attr) nor a sole non-region attribute standing in for it -
		// there is nothing to scope by, and the unscoped list of these
		// types (IAM/ListRolePolicies with no role) is an error, not an
		// enumeration. Nothing to do rather than a failed call every plan.
		return diags
	}

	declared := make(map[string]bool)
	for _, r := range res.Resolutions {
		if r.Type() != typeName || r.Class != identity.ClassConcrete {
			continue
		}
		declared[r.ImportID] = true
	}

	for _, r := range res.Resolutions {
		if r.Type() != link.Parent || r.Class != identity.ClassConcrete || r.ImportID == "" {
			continue
		}
		if modCfg, ok := identity.ConfigForModule(req.Config, r.Addr.Module); ok && modCfg.Module != nil {
			if rc, ok := modCfg.Module.ManagedResources[r.Addr.Resource.Resource.String()]; ok && !inScope(req.ScopeProvider, rc, modCfg) {
				// Issue #69's multi-provider sweep: a parent belonging to a
				// different provider configuration is read by that pass, not
				// this one.
				continue
			}
		}
		if parentHeldByOtherEstate(res, link.Parent, r.ImportID) {
			continue
		}
		diags = diags.Append(readParentListChildren(ctx, req, ts, typeName, link, scopeAttr, r.Addr, r.ImportID, declared, res))
	}
	return diags
}

// readParentListChildren lists one parent's children with a call scoped
// server-side to parentValue and records every undeclared child as a
// removal finding. Every result of a parent-scoped list belongs to that
// parent, so - unlike [readParentChildScoped]'s named-singleton match -
// each result's WHOLE identity is taken as the child's, and there is no
// single-child break: a parent may own many.
func readParentListChildren(ctx context.Context, req Request, ts listclient.TypeSchema, typeName string, link identity.ParentLink, scopeAttr string, parentAddr addrs.AbsResourceInstance, parentValue string, declared map[string]bool, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	vals := map[string]cty.Value{scopeAttr: cty.StringVal(parentValue)}
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
		// The same restraint the rest of this leg shows: a provider hiccup
		// on a type the configuration may not even mention must not fail
		// the run.
		return diags
	}
	diags = diags.Append(listDiags)

	for _, r := range results {
		importID, ok := childImportID(typeName, r)
		if !ok || declared[importID] {
			continue
		}
		recordListRecoveredFinding(typeName, link, parentAddr, parentValue, importID, r, res)
	}
	return diags
}

// recordListRecoveredFinding turns one undeclared parent-list-recovered
// child into a [ParentReadFinding] and, for a removable type, an undeclared
// [identity.Resolution] the plan destroys - the multi-component analogue of
// [recordParentReadFinding]. The synthetic address carries the child's own
// recovered name rather than the parent's, since one parent may own several.
func recordListRecoveredFinding(typeName string, link identity.ParentLink, parentAddr addrs.AbsResourceInstance, parentValue, importID string, r listclient.Result, res *Result) {
	_, idAttr, _ := importIdentity(typeName, r)
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
		res.Resolutions = append(res.Resolutions, identity.Resolution{
			Addr:       listRecoveredChildAddr(typeName, parentAddr, parentValue, importID),
			Class:      identity.ClassConcrete,
			ImportID:   importID,
			Identity:   r.Identity,
			Undeclared: true,
		})
	} else {
		finding.Withheld = fmt.Sprintf(
			"%s is parent-list-recoverable via %s but not wired for removal; see live/LIMITATIONS.md",
			typeName, link.Parent)
	}
	res.ParentReads = append(res.ParentReads, finding)
}

// listRecoveredChildAddr is the address a removable list-recovered finding
// enters the prior state at. A parent-list-recovered child has no marker
// and no declared block to read a name from - the block was deleted - so
// the label is the child's own recovered name (the identity segment past
// the parent's value: an inline policy's own policy name), best effort, the
// same status [syntheticChildAddr]'s doc gives its parent-named label. The
// destroy still targets the right live object; only the printed address may
// not match the deleted block's original name. It cannot collide with a
// declared instance, because this leg reaches here only for a child no
// declared resolution's identity claims.
func listRecoveredChildAddr(childType string, parentAddr addrs.AbsResourceInstance, parentValue, importID string) addrs.AbsResourceInstance {
	name := strings.TrimPrefix(importID, parentValue)
	name = strings.TrimLeft(name, ":/-")
	name = sanitizeAddrName(name)
	if name == "" {
		name = parentAddr.Resource.Resource.Name
	}
	return addrs.AbsResourceInstance{
		Module: parentAddr.Module,
		Resource: addrs.ResourceInstance{
			Resource: addrs.Resource{
				Mode: addrs.ManagedResourceMode,
				Type: childType,
				Name: name,
			},
		},
	}
}

// sanitizeAddrName reduces a recovered identity segment to a valid resource
// instance name: letters, digits, underscore and hyphen kept, everything
// else folded to underscore, and a leading digit prefixed so the label
// parses. It is a display label only - see [listRecoveredChildAddr].
func sanitizeAddrName(in string) string {
	var b strings.Builder
	for _, r := range in {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out != "" && out[0] >= '0' && out[0] <= '9' {
		out = "_" + out
	}
	return out
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
