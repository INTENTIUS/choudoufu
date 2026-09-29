// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package lint

import (
	"fmt"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/providers"
)

// checkIgnoreChanges rejects a lifecycle block that tells the run to discard
// changes to the tags the ownership markers live in.
//
// This is GitHub issue #103, and it is the quietest failure the live path
// had. The stamp pass writes tofu-estate and tofu-address into the
// resource's tags argument, the plan renders that as an in-place update, and
// then the core throws the change away because the author asked for tags to
// be ignored. Nothing warns. The resource is applied unmarked, the next
// run's discovery cannot find it, and every run after that proposes creating
// a duplicate of something that already exists.
//
// `ignore_changes = [tags]` is a common idiom, usually added for exactly the
// reason that makes it dangerous here: something outside Terraform writes
// tags on this resource. Under live markers, this tool is that something.
//
// Two shapes are refused and one is deliberately not:
//
//   - `ignore_changes = all` covers tags along with everything else.
//   - `ignore_changes = [tags]`, and `tags["tofu-estate"]` or any other
//     marker key named directly.
//   - `tags["Owner"]`, or any other non-marker key, is left alone. Ignoring
//     one tag this tool does not write is an ordinary thing to want, and
//     refusing it would be a refusal with no reason behind it.
//
// tags_all is not refused either. It is the provider's computed union of
// tags and the provider-level default_tags, so ignoring it does not stop the
// markers being written into tags, and the update still happens. That was
// checked rather than assumed: with ignore_changes = [tags_all] the markers
// survive in tags untouched, and with [tags] they are stripped.
//
// # Only where markers actually live
//
// An adversarial audit found the first version refusing three populations
// this rule has nothing to say about, so it now declines twice before it
// fires.
//
// A logical type is skipped outright. null_resource and terraform_data are
// admitted once a live block declares a record_store (#73's supported path),
// which silences RuleLogicalResource - and this rule then became their only
// refusal, explained as losing tags they do not have. Their identity is a
// persisted record, not a marker, and ignoring their tags costs nothing.
//
// An untaggable type is skipped when the provider schemas are available to
// say so: aws_route and aws_iam_role_policy_attachment carry no tags at all,
// and aws_autoscaling_group's tags are nested blocks rather than the map
// [markers.Taggable] describes, which the stamp pass never writes into
// either.
//
// Without schemas the second check cannot run, and the rule fires as if
// every non-logical managed type carried the AWS tags surface, which is
// the surface [substrate.SurfaceOf] falls back to below. That is the same
// asymmetry admitted() already has - a caller with no schemas gets a
// stricter answer, not a different one - and every command that actually
// stamps reads them first (internal/command/live_plan.go), so the residue
// is confined to a schema-less "choudoufu live-check", whose output
// already says which verdicts depend on them.
//
// # The label and manifest carriers (GitHub issue #1645)
//
// Ruled 2026-09-27: refuse, same as AWS. A Kubernetes type is never
// [markers.Taggable] - it has no tags map at all - so before this the
// schema check above sent every one of them home with nothing checked, and
// [markers.LabelSurfacePath] and [markers.ManifestLabelPath], the two
// carriers [projection.AdjustIgnoreChanges] already knows, went unguarded.
// The failure is #103's, on the label surface: the node stamp writes
// tofu-estate into metadata[0].labels (or manifest.metadata.labels), the
// plan renders that as an in-place update, and ignore_changes throws it
// away, so the estate label a migrated or newly created object needs is
// never applied and every run after that reads the object as unowned.
//
// With a schema in hand this dispatches on [substrate.SurfaceOf] rather
// than [markers.Taggable] directly, so a type is skipped only when its
// schema carries none of the family surfaces at all (the patch types -
// kubernetes_labels, kubernetes_config_map_v1_data - and the AWS types
// [markers.Taggable] already excluded). [checkIgnoreChangesLabel] is the
// per-surface check for the two Kubernetes shapes; [checkIgnoreChangesTags]
// is the AWS one above, unchanged.
func checkIgnoreChanges(resource *configs.Resource, addr string, path addrs.Module, schemas map[string]providers.Schema, markersRecord *strict.Selection, issues *[]Issue) {
	managed := resource.Managed
	if managed == nil {
		return
	}
	if _, logical := ClassifyLogicalType(resource.Type); logical {
		return
	}

	surface := markers.SurfaceTags
	if schema, ok := schemas[resource.Type]; ok {
		s, hasSurface := substrate.SurfaceOf(schema.Block)
		if !hasSurface {
			return
		}
		surface = s
	}

	// The third population this rule declines on, and the only one that is a
	// choice rather than a fact: GitHub issue #365's
	// `strict { marker_repair = "never"; markers "record" { ... } }`.
	//
	// Everything above this rule's refusal rests on is the marker being the
	// resource's identity - "the update that writes them is planned and then
	// discarded", so "adopting a resource this configuration does not yet own
	// can never succeed", so "a marker that drifts can never be repaired".
	// For a resource this selection covers, none of those three sentences is
	// true any more. No marker is written into its tags at all
	// (internal/live/stamp's SkipMarkersRecord), so there is no update to
	// discard; its identity is the record the estate's store holds
	// (identity.ClassRecordLocated), so there is nothing to adopt by tag and
	// nothing to drift. Ignoring its tags costs exactly what ignoring them
	// costs on stock, which is HANDOFF.md's own phrasing for this toggle:
	// "with ignore_changes honoured exactly as stock honours it".
	//
	// Both halves are required and [markerRepairHonoursIgnoreChanges] is
	// where that is argued. What matters here is the direction of the
	// asymmetry: markersRecord is nil for every configuration that does not
	// set both, and this rule then fires exactly as it did before - including
	// on the resources a selection covers when the schemas were not available
	// to prove the selection could be honoured, which is the same
	// stricter-without-schemas answer the taggability check above already
	// gives.
	if markersRecord.Selects(addrs.ConfigResource{Module: path, Resource: resource.Addr()}) &&
		identity.SelectedLocatedType(resource.Type, schemas) {
		return
	}

	switch surface {
	case markers.SurfaceLabels:
		checkIgnoreChangesLabel(resource, addr, path, markers.LabelSurfacePath(markers.TagEstate), issues)
	case markers.SurfaceManifest:
		checkIgnoreChangesLabel(resource, addr, path, markers.ManifestLabelPath(markers.TagEstate), issues)
	default:
		checkIgnoreChangesTags(resource, addr, path, issues)
	}
}

// checkIgnoreChangesTags is the AWS tags-surface check [checkIgnoreChanges]
// ran inline before GitHub issue #1645 split it out to make room for
// [checkIgnoreChangesLabel] beside it. Unchanged in every particular: the
// wording, the whole-argument and per-key split, and the diagnostic ranges.
func checkIgnoreChangesTags(resource *configs.Resource, addr string, path addrs.Module, issues *[]Issue) {
	managed := resource.Managed

	if managed.IgnoreAllChanges {
		*issues = append(*issues, Issue{
			Rule:      RuleIgnoreChanges,
			Construct: fmt.Sprintf("lifecycle { ignore_changes = all } on %s", addr),
			Module:    path,
			Detail: fmt.Sprintf(
				"%s ignores every change, which includes the %s and %s tags this mode writes to record ownership. "+
					"An existing resource would keep whatever markers it has, or none: the update that writes them is "+
					"planned and then discarded, so adopting a resource this configuration does not yet own can never "+
					"succeed, and a marker that drifts can never be repaired. "+
					"Narrow ignore_changes to the arguments you actually mean, leaving tags out of it.",
				addr, markers.TagEstate, markers.TagAddress,
			),
			Subject: resource.DeclRange,
		})
		return
	}

	for _, traversal := range managed.IgnoreChanges {
		key, whole, ok := ignoredTagKey(traversal)
		if !ok {
			continue
		}
		if !whole && !isMarkerTag(key) {
			continue
		}

		construct := fmt.Sprintf("lifecycle { ignore_changes = [tags] } on %s", addr)
		detail := fmt.Sprintf(
			"%s ignores changes to its whole tags argument, and the %s and %s tags this mode writes to record ownership live there. "+
				"An existing resource would keep whatever markers it has, or none: the update that writes them is planned "+
				"and then discarded, so adopting a resource this configuration does not yet own can never succeed, and a "+
				"marker that drifts can never be repaired. "+
				"Ignore the individual tag keys something outside this configuration writes - ignore_changes = [tags[\"Owner\"]] - "+
				"rather than the whole argument.",
			addr, markers.TagEstate, markers.TagAddress,
		)
		if !whole {
			construct = fmt.Sprintf("lifecycle { ignore_changes = [tags[%q]] } on %s", key, addr)
			detail = fmt.Sprintf(
				"%s ignores changes to the %q tag, which is one of the ownership markers this mode writes. "+
					"The update that writes it is planned and then discarded, so an existing resource can never be "+
					"adopted and a marker that drifts can never be repaired. Ownership markers are not an argument a "+
					"configuration manages; remove this entry.",
				addr, key,
			)
		}

		*issues = append(*issues, Issue{
			Rule:      RuleIgnoreChanges,
			Construct: construct,
			Module:    path,
			Detail:    detail,
			Subject:   traversalRange(traversal, resource.DeclRange),
		})
	}
}

// checkIgnoreChangesLabel is the Kubernetes counterpart of
// [checkIgnoreChangesTags], GitHub issue #1645's fix: the same refusal, for
// a resource whose marker lives in a labels map rather than a tags map.
// markerPath is the full cty.Path of the one marker this surface carries -
// [markers.LabelSurfacePath] for SurfaceLabels, [markers.ManifestLabelPath]
// for SurfaceManifest - always ending in the tofu-estate index step, since
// neither Kubernetes label map carries tofu-address
// ([substrate.AddressInMarkers] is false for both; the address rides in an
// annotation, GitHub issue #1641).
//
// An ignore_changes entry is refused when its own path is a PREFIX of
// markerPath (including the whole path, which is the entry naming the
// marker key itself): ignoring metadata, or metadata[0].labels, throws away
// the update that writes metadata[0].labels["tofu-estate"] exactly as
// surely as naming that key directly does. An entry rooted anywhere else -
// a different label key, a different top-level argument - is left alone,
// the same "not this rule's business" answer the tags check gives
// tags["Owner"].
func checkIgnoreChangesLabel(resource *configs.Resource, addr string, path addrs.Module, markerPath cty.Path, issues *[]Issue) {
	managed := resource.Managed
	carrier := pathString(markerPath[:len(markerPath)-1])

	if managed.IgnoreAllChanges {
		*issues = append(*issues, Issue{
			Rule:      RuleIgnoreChanges,
			Construct: fmt.Sprintf("lifecycle { ignore_changes = all } on %s", addr),
			Module:    path,
			Detail: fmt.Sprintf(
				"%s ignores every change, which includes the %s label this mode writes at %s to record ownership. "+
					"An existing object would keep whatever label it has, or none: the update that writes it is "+
					"planned and then discarded, so adopting an object this configuration does not yet own can never "+
					"succeed, and a label that drifts can never be repaired. "+
					"Narrow ignore_changes to the arguments you actually mean, leaving %s out of it.",
				addr, markers.TagEstate, carrier, carrier,
			),
			Subject: resource.DeclRange,
		})
		return
	}

	for _, traversal := range managed.IgnoreChanges {
		travPath, ok := traversalToCtyPath(traversal)
		if !ok || !pathHasPrefix(markerPath, travPath) {
			continue
		}

		entry := pathString(travPath)
		construct := fmt.Sprintf("lifecycle { ignore_changes = [%s] } on %s", entry, addr)
		var detail string
		if len(travPath) < len(markerPath) {
			detail = fmt.Sprintf(
				"%s ignores changes to %s, and the %s label this mode writes at %s to record ownership lives "+
					"underneath it. An existing object would keep whatever label it has, or none: the update that "+
					"writes it is planned and then discarded, so adopting an object this configuration does not yet "+
					"own can never succeed, and a label that drifts can never be repaired. "+
					"Narrow ignore_changes to the arguments you actually mean, leaving %s out of it.",
				addr, entry, markers.TagEstate, carrier, carrier,
			)
		} else {
			detail = fmt.Sprintf(
				"%s ignores changes to the %s label, which is the ownership marker this mode writes. "+
					"The update that writes it is planned and then discarded, so an existing object can never be "+
					"adopted and a label that drifts can never be repaired. Ownership markers are not an argument a "+
					"configuration manages; remove this entry.",
				addr, markers.TagEstate,
			)
		}

		*issues = append(*issues, Issue{
			Rule:      RuleIgnoreChanges,
			Construct: construct,
			Module:    path,
			Detail:    detail,
			Subject:   traversalRange(traversal, resource.DeclRange),
		})
	}
}

// ignoredTagKey reads one ignore_changes traversal.
//
// It returns whole=true for a bare `tags`, and whole=false with the key for
// `tags["k"]` or `tags.k`. ok is false for a traversal that is not rooted at
// tags at all, and for `tags[<non-literal>]`, which cannot be read here -
// that last case is reported as covering the whole argument, because a key
// this pass cannot evaluate might be a marker key.
func ignoredTagKey(traversal hcl.Traversal) (key string, whole, ok bool) {
	if len(traversal) == 0 {
		return "", false, false
	}
	root, isAttr := traversal[0].(hcl.TraverseAttr)
	if !isAttr || root.Name != "tags" {
		return "", false, false
	}
	if len(traversal) == 1 {
		return "", true, true
	}

	switch step := traversal[1].(type) {
	case hcl.TraverseIndex:
		// A constant of any type: hcl.RelTraversalForExpr refuses a
		// non-constant index while decoding ignore_changes
		// (internal/configs/resource.go), so a variable key never reaches
		// here and the "unreadable key" case this branch was first written
		// for does not exist. What does reach it is a non-string constant
		// such as tags[0], which names the tag key "0" - not a marker, and
		// not the whole argument either.
		str, err := convert.Convert(step.Key, cty.String)
		if err != nil || str.IsNull() || str.IsMarked() {
			// IsMarked before AsString, which panics on a marked value.
			// Unreachable while hcl only builds a TraverseIndex from a
			// source constant, as the comment above says; tested so that
			// stops being load-bearing.
			return "", false, false
		}
		return str.AsString(), false, true
	case hcl.TraverseAttr:
		return step.Name, false, true
	default:
		// A traversal into tags this pass does not recognise. It is not the
		// whole argument, so treating it as one would refuse something with
		// no reason given; leaving it alone is the same answer the rule
		// gives every other non-marker key.
		return "", false, false
	}
}

// isMarkerTag reports whether a tag key is one this mode writes: the three
// markers, plus the tofu-address continuations an overlong address is split
// across (live/MARKERS.md).
func isMarkerTag(key string) bool {
	switch key {
	case markers.TagEstate, markers.TagAddress, markers.TagSlot:
		return true
	}
	return strings.HasPrefix(key, markers.TagAddress+"-")
}

// traversalRange points the diagnostic at the ignore_changes entry itself
// where the traversal carries a range, and at the resource block otherwise.
func traversalRange(traversal hcl.Traversal, fallback hcl.Range) hcl.Range {
	if rng := traversal.SourceRange(); rng.Filename != "" {
		return rng
	}
	return fallback
}

// traversalToCtyPath converts one ignore_changes traversal into the cty.Path
// [checkIgnoreChangesLabel] compares against a marker path. Every traversal
// this pass sees is the relative shape hcl.RelTraversalForExpr builds while
// decoding ignore_changes (internal/configs/resource.go): rooted at a
// TraverseAttr rather than a TraverseRoot, with TraverseIndex for a `[...]`
// step. ok is false for anything else - there is no third step kind
// RelTraversalForExpr produces, so this is defensive rather than reachable,
// and false is the same "leave it alone" answer [ignoredTagKey]'s own
// default case gives an unrecognised traversal.
func traversalToCtyPath(traversal hcl.Traversal) (cty.Path, bool) {
	path := make(cty.Path, 0, len(traversal))
	for _, step := range traversal {
		switch ts := step.(type) {
		case hcl.TraverseAttr:
			path = append(path, cty.GetAttrStep{Name: ts.Name})
		case hcl.TraverseIndex:
			path = append(path, cty.IndexStep{Key: ts.Key})
		default:
			return nil, false
		}
	}
	return path, true
}

// pathHasPrefix reports whether prefix is a prefix of path, inclusive of
// prefix == path: an ignore_changes entry refuses the marker exactly when
// its own path prefixes the marker's, because ignoring a shorter path also
// throws away everything nested under it.
func pathHasPrefix(path, prefix cty.Path) bool {
	if len(prefix) > len(path) {
		return false
	}
	for i, step := range prefix {
		if !pathStepsEqual(step, path[i]) {
			return false
		}
	}
	return true
}

// pathStepsEqual compares two cty.PathStep values of the same two kinds
// [traversalToCtyPath] and the markers package's *Path functions ever
// build: an attribute name, or an index key compared with RawEquals (the
// numeric block index and the string label key both round-trip through it
// cleanly, since both sides are built the same way - HCL's own constant
// folding on one side, cty.NumberIntVal/cty.StringVal on the other).
func pathStepsEqual(a, b cty.PathStep) bool {
	switch as := a.(type) {
	case cty.GetAttrStep:
		bs, ok := b.(cty.GetAttrStep)
		return ok && as.Name == bs.Name
	case cty.IndexStep:
		bs, ok := b.(cty.IndexStep)
		return ok && as.Key.RawEquals(bs.Key)
	default:
		return false
	}
}

// pathString renders a cty.Path the way an operator would write it in an
// ignore_changes entry: dotted attribute steps, bracketed index steps -
// "metadata[0].labels", "metadata[0].labels[\"tofu-estate\"]",
// "manifest.metadata.labels" - for the construct and detail text
// [checkIgnoreChangesLabel] builds from a traversal or a marker path alike.
func pathString(p cty.Path) string {
	var b strings.Builder
	for i, step := range p {
		switch s := step.(type) {
		case cty.GetAttrStep:
			if i > 0 {
				b.WriteByte('.')
			}
			b.WriteString(s.Name)
		case cty.IndexStep:
			// A marker path never carries a mark ([markers.LabelSurfacePath]
			// and [markers.ManifestLabelPath] build the key with
			// cty.StringVal/cty.NumberIntVal) and neither does a
			// traversal's own constant index ([traversalToCtyPath] takes it
			// straight off HCL's constant folding), but this renders the
			// key back out for a diagnostic rather than proving either
			// producer, so the cheap answer is to refuse the read outright
			// like every other guarded call in this package does.
			if s.Key.IsMarked() {
				b.WriteString("[...]")
				continue
			}
			if s.Key.Type() == cty.String {
				fmt.Fprintf(&b, "[%q]", s.Key.AsString())
				continue
			}
			f := s.Key.AsBigFloat()
			n, _ := f.Int64()
			fmt.Fprintf(&b, "[%d]", n)
		}
	}
	return b.String()
}
