// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
)

// This file composes the rewrites hashicorp/google applies to an argument
// before it becomes part of the id (#2144), so that a row reproduces the id
// the provider builds rather than the value as written. Each rule is
// [LongFormRule]'s, read from the v8.6.0 source by tools/gcp-row-gen:
//
//   - tpgresource.ReplaceVarsForId keeps the last path segment of an
//     ordinary argument and trims the scope prefixes, inline, per component.
//   - tpgresource.GetImportIdQualifiers parses an IAM parent against the
//     updater's regexes and lets what it captures replace the parent's other
//     components, after every component has resolved.
//   - a computed-name helper (pubsub's GetComputedSubscriptionName) keeps a
//     full resource name whole and templates anything else.
//
// The last two read sibling components, which is why resolveInstance holds
// each component as a [segment] until the whole row has resolved.

// segment is one component's contribution to an import ID.
type segment struct {
	// comp is the component's index in the row.
	comp int
	// attr is the identity attribute the segment supplies, if any.
	attr string
	// arg is the argument it was read from, empty for a literal or a
	// cloud value. It is also the placeholder name a qualifier's named
	// group is matched against.
	arg string
	// parts is the resolved value.
	parts []Part
	// prefix is a [Component.Literal] that precedes the value in the import
	// ID string and is not part of the identity attribute.
	prefix string
	// rng is where a refusal about this segment points.
	rng hcl.Range
	// idParts, when non-nil, is what the segment contributes to the
	// import ID in place of prefix and parts, which still supply its
	// identity attribute: an azapi_resource's type rewrites the parent_id
	// segment before it into the text azapi joins it with (GitHub issue
	// #1120, [Component.ARMTypeSegment]).
	idParts []Part
}

// placeholder is the name the provider's template calls this segment by:
// the argument it was read from, or the cloud property it fell back to.
func (sg segment) placeholder(entry TypeIdentity) string {
	if sg.arg != "" {
		return sg.arg
	}
	return string(entry.Components[sg.comp].Cloud)
}

// literal is the segment's value when every part of it is known.
func literal(parts []Part) (string, bool) {
	var b strings.Builder
	for _, p := range parts {
		if p.Parent != nil {
			return "", false
		}
		b.WriteString(p.Literal)
	}
	return b.String(), true
}

// longFormValue is the per-component half of [Component.LongForm]. It
// refuses a value only an apply can know, because every rewrite reads the
// value's text and a formula cannot carry it (#2144). For
// [LongFormLastSegment] it returns the value ReplaceVarsForId substitutes;
// the two IAM rules return the value unchanged and are composed by
// [resolver.composeLongForms] once the whole row has resolved.
func (r *resolver) longFormValue(comp Component, addr addrs.AbsResourceInstance, attr *hcl.Attribute, got []Part) ([]Part, bool) {
	if comp.LongForm == "" {
		return got, true
	}
	v, known := literal(got)
	if !known {
		var parent string
		for _, p := range got {
			if p.Parent != nil {
				parent = p.Parent.String()
				break
			}
		}
		r.errorf(attr.Range, "Rewritten identity argument not known before apply",
			"%s sets %s from %s, which is not known until apply. %s, which reads the value's text, so the id cannot be "+
				"built until the value exists and this fork will not guess its form (#2144). Write the parent's name "+
				"rather than its id, or a literal, to resolve it now.",
			addr.String(), attr.Name, parent, comp.LongForm.describe())
		return nil, false
	}
	if comp.LongForm == LongFormEnsurePrefix {
		if len(comp.Qualifiers) != 1 {
			r.errorf(attr.Range, "Rewritten identity argument not composable",
				"%s sets %s to %q. %s, and this fork did not read which prefix, so the instance is refused rather than given an id (#2145).",
				addr.String(), attr.Name, v, comp.LongForm.describe())
			return nil, false
		}
		if v == "" || v == comp.Qualifiers[0] {
			r.errorf(attr.Range, "Rewritten identity argument not composable",
				"%s sets %s to %q. %s (%q), which leaves nothing after it, and a parent id with an empty segment names no "+
					"object, so the instance is refused rather than given one (#2145).",
				addr.String(), attr.Name, v, comp.LongForm.describe(), comp.Qualifiers[0])
			return nil, false
		}
		// The value itself is unchanged: [ensuredPrefix] adds the prefix
		// to the import ID alone.
		return got, true
	}
	if comp.LongForm == LongFormTrimPrefix {
		if len(comp.Qualifiers) != 1 {
			r.errorf(attr.Range, "Rewritten identity argument not composable",
				"%s sets %s to %q. %s, and this fork did not read which prefix, so the instance is refused rather than given an id (#2145).",
				addr.String(), attr.Name, v, comp.LongForm.describe())
			return nil, false
		}
		short := strings.TrimPrefix(v, comp.Qualifiers[0])
		if short == "" {
			r.errorf(attr.Range, "Rewritten identity argument not composable",
				"%s sets %s to %q. %s (%q), which leaves nothing of this value, and an id with an empty segment names no "+
					"object, so the instance is refused rather than given one (#2145).",
				addr.String(), attr.Name, v, comp.LongForm.describe(), comp.Qualifiers[0])
			return nil, false
		}
		return []Part{{Literal: short}}, true
	}
	if comp.LongForm != LongFormLastSegment {
		return got, true
	}
	short := forIDValue(attr.Name, v, true)
	if short == "" {
		r.errorf(attr.Range, "Rewritten identity argument not composable",
			"%s sets %s to %q. %s, which leaves nothing of this value, and an id with an empty segment names no "+
				"object, so the instance is refused rather than given one (#2144).",
			addr.String(), attr.Name, v, comp.LongForm.describe())
		return nil, false
	}
	return []Part{{Literal: short}}, true
}

// forIDValue is tpgresource.BuildReplacementFunc at v8.6.0 with shorten
// set, for one placeholder. fromResource says the value is the resource's
// own argument rather than the provider's default: GetRegion and GetZone
// take the last segment of the former only, and ReplaceVarsForId then trims
// the scope prefix off either.
func forIDValue(name, v string, fromResource bool) string {
	switch name {
	case "project":
		return strings.TrimPrefix(v, "projects/")
	case "region":
		if fromResource {
			v = lastPathSegment(v)
		}
		return strings.TrimPrefix(v, "regions/")
	case "zone":
		if fromResource {
			v = lastPathSegment(v)
		}
		return strings.TrimPrefix(v, "zones/")
	}
	return lastPathSegment(v)
}

// lastPathSegment is tpgresource.GetResourceNameFromSelfLink.
func lastPathSegment(v string) string {
	parts := strings.Split(v, "/")
	return parts[len(parts)-1]
}

// composeLongForms is the row-level half of [Component.LongForm]: an IAM
// parent's argument may name its siblings too. It returns the segments the
// provider's own rewrite leaves, or false after refusing the instance by
// name where the provider's updater would itself fail, or would replan the
// resource on every run.
func (r *resolver) composeLongForms(entry TypeIdentity, segs []segment, addr addrs.AbsResourceInstance) ([]segment, bool) {
	for q := 0; q < len(segs); q++ {
		sg := segs[q]
		comp := entry.Components[sg.comp]
		if comp.LongForm != LongFormIAMParent && comp.LongForm != LongFormComputedName {
			continue
		}
		v, _ := literal(sg.parts)
		res, ok := r.composeOne(entry, segs, q, v, addr)
		if !ok {
			return nil, false
		}
		segs = res
		break
	}
	return segs, true
}

// composeOne applies segs[q]'s rule, whose resolved value is v.
func (r *resolver) composeOne(entry TypeIdentity, segs []segment, q int, v string, addr addrs.AbsResourceInstance) ([]segment, bool) {
	sg := segs[q]
	comp := entry.Components[sg.comp]
	refuse := func(why string) ([]segment, bool) {
		r.errorf(sg.rng, "Rewritten identity argument not composable",
			"%s sets %s to %q. %s, and %s, so the instance is refused by name rather than given an id (#2144).",
			addr.String(), sg.arg, v, comp.LongForm.describe(), why)
		return nil, false
	}
	res := append([]segment(nil), segs...)

	switch comp.LongForm {
	case LongFormComputedName:
		if len(comp.Qualifiers) != 1 {
			return refuse("this fork did not read the provider's full-name pattern for it")
		}
		re, err := regexp.Compile(comp.Qualifiers[0])
		if err != nil {
			return refuse("the provider's full-name pattern does not compile here")
		}
		if !re.MatchString(v) {
			// Placed into the parent template, which the row's own
			// components already spell.
			return res, true
		}
		// The whole parent as written. Where it is exactly the row's
		// template it is told as the template's pieces, so that the
		// subscription's .id and its short name give one identity and
		// a duplicate between them is still caught; anything else
		// replaces every segment the provider did not use.
		if m, names, ok := templateMatch(entry, res[:q+1], v); ok {
			setCaptured(entry, res, q, m, names)
			return res, true
		}
		whole := sg
		whole.prefix = ""
		whole.parts = []Part{{Literal: v}}
		return append([]segment{whole}, res[q+1:]...), true

	case LongFormIAMParent:
		if len(comp.Qualifiers) == 0 {
			return refuse("this fork did not read the provider's qualifier list for it")
		}
		for _, pattern := range comp.Qualifiers {
			re, err := regexp.Compile(pattern)
			if err != nil {
				return refuse("the provider's qualifier " + pattern + " does not compile here")
			}
			m := re.FindStringSubmatch(v)
			if m == nil {
				continue
			}
			if why := argConflict(entry, res, q, m, re.SubexpNames()); why != "" {
				return refuse(why)
			}
			setCaptured(entry, res, q, m, re.SubexpNames())
			return res, true
		}
		return refuse("it matches none of the formats the provider's updater accepts, which is the updater's own error")
	}
	return res, true
}

// argConflict is why a match cannot be composed, or "" when it can. The
// updater writes each captured value back to its argument with d.Set, so a
// long form naming another project, or another location, than the one the
// resource sets plans a replacement on every run. A provider default is no
// conflict: GetImportIdQualifiers fills one in only where the long form
// names nothing, and the long form wins.
func argConflict(entry TypeIdentity, res []segment, q int, m, names []string) string {
	for k := 1; k < len(m) && k < len(names); k++ {
		for j := 0; j < q; j++ {
			if res[j].arg == "" || !res[j].reads(entry, names[k]) {
				continue
			}
			if was, _ := literal(res[j].parts); was != m[k] {
				return fmt.Sprintf("it names %s %q where the resource sets %s to %q, which the updater overwrites with the long form's on every read", names[k], m[k], res[j].arg, was)
			}
		}
	}
	return ""
}

// reads reports whether the segment's component is the one a captured
// group of this name fills: by an argument it reads, or by the cloud
// property it falls back to.
func (sg segment) reads(entry TypeIdentity, name string) bool {
	if name == "" {
		return false
	}
	comp := entry.Components[sg.comp]
	if string(comp.Cloud) == name {
		return true
	}
	for _, a := range comp.Attrs {
		if a == name {
			return true
		}
	}
	return false
}

// setCaptured writes each named group of a match over the parent segment
// that reads it, res[0] through res[q]; a group naming none of them is
// a value the provider's GetResourceId never reads either. A segment that
// supplied no identity attribute (a provider default) gains one only when
// the captured value differs from it, so that `.id` and the short name of
// one parent keep one identity, and a long form naming another project
// says so.
func setCaptured(entry TypeIdentity, res []segment, q int, m, names []string) {
	for k := 1; k < len(m) && k < len(names); k++ {
		name := names[k]
		for j := 0; j <= q; j++ {
			if !res[j].reads(entry, name) {
				continue
			}
			was, _ := literal(res[j].parts)
			res[j].parts = []Part{{Literal: m[k]}}
			if res[j].attr == "" && was != m[k] {
				res[j].attr = entry.Components[res[j].comp].identityAttrFor(name)
			}
		}
	}
}

// templateMatch matches v against the template the parent segments spell,
// literal for literal and one path segment per placeholder, anchored at
// both ends.
func templateMatch(entry TypeIdentity, parent []segment, v string) ([]string, []string, bool) {
	var b strings.Builder
	b.WriteString("^")
	for _, sg := range parent {
		comp := entry.Components[sg.comp]
		if len(comp.Attrs) == 0 && comp.Cloud == CloudNone {
			b.WriteString(regexp.QuoteMeta(comp.Literal))
			continue
		}
		b.WriteString("(?P<" + sg.placeholder(entry) + ">[^/]+)")
	}
	b.WriteString("$")
	re, err := regexp.Compile(b.String())
	if err != nil {
		return nil, nil, false
	}
	m := re.FindStringSubmatch(v)
	if m == nil {
		return nil, nil, false
	}
	return m, re.SubexpNames(), true
}

// ensuredPrefix is the import-ID prefix a component contributes before its
// value: its own [Component.Literal], followed for [LongFormEnsurePrefix]
// by the prefix the provider's helper adds when the value lacks it. got
// has already been through [resolver.longFormValue], so it is known.
func ensuredPrefix(comp Component, got []Part) string {
	if comp.LongForm != LongFormEnsurePrefix || len(comp.Qualifiers) != 1 {
		return comp.Literal
	}
	if v, _ := literal(got); !strings.HasPrefix(v, comp.Qualifiers[0]) {
		return comp.Literal + comp.Qualifiers[0]
	}
	return comp.Literal
}
