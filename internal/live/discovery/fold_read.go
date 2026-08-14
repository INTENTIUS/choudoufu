// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// The fold-child leg (issue #68): a generalization of parent_read.go's
// named-singleton-child sweep to a declared property-child whose whole
// identity duplicates an already-admitted parent's own identity Components
// verbatim, rather than one scalar argument. See
// internal/live/identity/parent.go's FoldParentTypes/FoldParentOf doc
// comment for why this needs its own leg instead of a broadened
// [identity.SingleParentComponent]: the parent here (aws_api_gateway_method)
// may itself be untaggable and [identity.ClassParentDerived], which is
// outside the "taggable, concrete parent" model parent_read.go's original
// leg is built on.
package discovery

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// foldChildReadSweep is the leg's entry point, called once per [Discover]
// pass that asked for a sweep, alongside [parentReadSweep]. It costs one
// list call per (fold-child type, declared parent instance whose own
// identity this pass could fully render) - the same bound
// [parentReadSweepType]'s scoped branch holds itself to.
//
// The parent's own identity is rendered right here, inside this pass,
// rather than waiting for the later phase that would normally turn a
// [identity.ClassParentDerived] resolution's [identity.Formula] into a live
// import ID: [identity.Formula.RenderAttrs] needs the same
// (parent-instance, attribute) -> value lookup a marker read would supply,
// and by the point this leg runs - after bind and classifyOrphans, the same
// place [parentReadSweep] runs - res.Resolutions already carries every
// parent value a marker sweep this same pass found. Rendering here, instead
// of waiting, is what lets an untaggable, composite parent
// (aws_api_gateway_method) anchor a read at all.
//
// One level deep only, for this first slice: a fold-child's parent must
// itself resolve to a marker-bound, [identity.ClassConcrete] value (or
// already be concrete from configuration alone) for [renderIdentityValues]'s
// lookup to supply it. A parent whose own parent is not yet settled is
// skipped rather than chased recursively - every fold-child this batch
// ratifies is exactly one level from a taggable grandparent
// (aws_api_gateway_method's own rest_api_id resolves through
// aws_api_gateway_rest_api, which the ordinary marker sweep this same
// [Discover] call already ran settles first), so this bound is not yet a
// real limit.
func foldChildReadSweep(ctx context.Context, req Request, schemas listclient.Schemas, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	byAddr := make(map[string]identity.Resolution, len(res.Resolutions))
	for _, r := range res.Resolutions {
		byAddr[r.Addr.String()] = r
	}
	lookup := func(parent addrs.AbsResourceInstance, attr string) (string, bool) {
		pr, ok := byAddr[parent.String()]
		if !ok || pr.Class != identity.ClassConcrete {
			return "", false
		}
		// A marker-bound resolution's ImportID is the live value a list
		// result's identity object carried under whichever of the parent
		// type's own IdentityAttrs importIdentity preferred - "id" for
		// every parent [FoldParentTypes] names today, the only attribute a
		// [identity.ParentRef] in this table ever asks for. Every entry
		// this map may grow to needs the same single-IdentityAttrs shape
		// checked before it is added.
		return pr.ImportID, true
	}

	folds := identity.FoldParentTypes()
	typeNames := make([]string, 0, len(folds))
	for t := range folds {
		typeNames = append(typeNames, t)
	}
	sort.Strings(typeNames)

	for _, typeName := range typeNames {
		diags = diags.Append(foldChildReadSweepType(ctx, req, schemas, typeName, folds[typeName], lookup, res))
	}
	return diags
}

// foldChildReadSweepType runs the leg for one (fold-child type, fold parent
// type) pair.
func foldChildReadSweepType(ctx context.Context, req Request, schemas listclient.Schemas, typeName, parentType string, lookup func(addrs.AbsResourceInstance, string) (string, bool), res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	ts, ok := schemas.Get(typeName)
	if !ok || markerCapable(ts) {
		// Not listable at all, or taggable - the latter meaning the
		// ordinary marker sweep already covers it and this leg has nothing
		// to add, the same test [parentReadSweep] applies.
		return diags
	}
	parentEntry, ok := identity.LookupType(parentType)
	if !ok {
		return diags
	}
	attrNames := componentAttrNames(parentEntry)
	if len(attrNames) == 0 {
		return diags
	}

	// Every declared instance of typeName, keyed by its own rendered tuple,
	// so an already-declared child is never reported as an orphan of its
	// own parent - the same declared-instance exclusion
	// [parentReadSweepType] applies for a named-singleton child, keyed here
	// on the whole tuple instead of one parent value.
	declared := make(map[string]bool)
	for _, r := range res.Resolutions {
		if r.Type() != typeName {
			continue
		}
		vals, ok := renderIdentityValues(r, lookup)
		if !ok {
			continue
		}
		declared[tupleKey(attrNames, vals)] = true
	}

	for _, r := range res.Resolutions {
		if r.Type() != parentType || r.Undeclared {
			continue
		}
		vals, ok := renderIdentityValues(r, lookup)
		if !ok {
			// The parent's own identity is not fully settled by this pass -
			// nothing to scope a read to. See the package doc comment's
			// "one level deep only" note.
			continue
		}
		if declared[tupleKey(attrNames, vals)] {
			continue
		}
		found, ok := readFoldChild(ctx, req, ts, typeName, vals)
		if !ok {
			continue
		}
		recordFoldReadFinding(typeName, parentType, r.Addr, attrNames, vals, found, res)
	}
	return diags
}

// renderIdentityValues is one resolution's identity as a map of argument
// name to value: [identity.Resolution.IdentityValues] directly for a
// concrete resolution, or [identity.Formula.RenderAttrs] against lookup for
// a parent-derived one. False when neither is available - an entry whose
// Components carry no per-attribute split at all, or a formula
// [identity.Formula.RenderAttrs] cannot fully render from what lookup
// already knows.
func renderIdentityValues(r identity.Resolution, lookup func(addrs.AbsResourceInstance, string) (string, bool)) (map[string]string, bool) {
	switch r.Class {
	case identity.ClassConcrete:
		if len(r.IdentityValues) == 0 {
			return nil, false
		}
		return r.IdentityValues, true
	case identity.ClassParentDerived:
		return r.Formula.RenderAttrs(lookup)
	default:
		return nil, false
	}
}

// componentAttrNames is the argument names entry's own Components read, in
// order - the set [renderIdentityValues] fills in and [readFoldChild] scopes
// its list config to. Every fold-child and fold-parent entry in the table
// names exactly one alternative per component (plain attr(name), never
// attr(a, b)), so the first (and only) alternative is always the right one.
func componentAttrNames(entry identity.TypeIdentity) []string {
	var out []string
	for _, c := range entry.Components {
		if len(c.Attrs) == 0 {
			continue
		}
		out = append(out, c.Attrs[0])
	}
	return out
}

// tupleKey joins a rendered identity's values, in names' order, into one
// comparable string - a NUL separator, since none of these arguments (a
// REST API ID, an HTTP method, a status code) can contain one.
func tupleKey(names []string, vals map[string]string) string {
	parts := make([]string, len(names))
	for i, n := range names {
		parts[i] = vals[n]
	}
	return strings.Join(parts, "\x00")
}

// readFoldChild scopes one list call to typeName's whole identity - vals,
// keyed by the same argument names the type's own list configuration
// accepts - and reports the first result, if any. A fold-child's identity
// leaves no room for a second live instance under the same tuple (API
// Gateway allows exactly one integration per method, the same way AMP
// allows exactly one alert manager definition per workspace), so the first
// result is the only one there ever is.
func readFoldChild(ctx context.Context, req Request, ts listclient.TypeSchema, typeName string, vals map[string]string) (listclient.Result, bool) {
	cfgVals := make(map[string]cty.Value, len(vals)+1)
	for name, v := range vals {
		if hasAttr(ts.Config, name) {
			cfgVals[name] = cty.StringVal(v)
		}
	}
	if hasAttr(ts.Config, "region") && req.Region != "" {
		cfgVals["region"] = cty.StringVal(req.Region)
	}
	config, cfgDiags := ts.BuildConfig(cfgVals)
	if cfgDiags.HasErrors() {
		return listclient.Result{}, false
	}
	results, listDiags := listclient.List(ctx, req.Provider, typeName, config, true)
	if listDiags.HasErrors() || len(results) == 0 {
		// Same restraint as [readParentChildScoped]: a provider hiccup, or a
		// clean "not found", on a type nothing in the configuration
		// mentions must not fail the whole run and is not itself evidence
		// of anything - the fold-child simply is not there.
		return listclient.Result{}, false
	}
	return results[0], true
}

// recordFoldReadFinding turns one match into a [ParentReadFinding], the same
// shared shape [recordParentReadFinding]'s named-singleton-child matches
// use. Report-only for every type [identity.FoldParentTypes] names today:
// none is in [identity.ParentReadRemovable]'s set, the same "unverified
// against a real not-found response" caution that keeps aws_sns_topic_policy
// and aws_sqs_queue_policy report-only despite fitting
// [identity.SingleParentComponent] cleanly.
func recordFoldReadFinding(typeName, parentType string, parentAddr addrs.AbsResourceInstance, attrNames []string, vals map[string]string, r listclient.Result, res *Result) {
	parts := make([]string, len(attrNames))
	for i, n := range attrNames {
		parts[i] = vals[n]
	}
	parentValue := strings.Join(parts, "/")

	importID, idAttr, hasID := importIdentity(typeName, r)
	if !hasID {
		// A fold-child that exports no additional attributes of its own
		// (aws_api_gateway_integration: "This resource exports no
		// additional attributes") has no list-result identity to read -
		// the rendered tuple is already the whole of what identifies it,
		// so it stands in for the import ID an operator-facing line prints.
		importID = parentValue
	}

	finding := ParentReadFinding{
		TypeName:     typeName,
		Parent:       parentType,
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
			"%s is reachable through %s's own identity but not yet wired for removal by this pass; see live/LIMITATIONS.md, \"Some are swept via a parent read instead (issue #60)\"",
			typeName, parentType)
	}
	res.ParentReads = append(res.ParentReads, finding)
}
