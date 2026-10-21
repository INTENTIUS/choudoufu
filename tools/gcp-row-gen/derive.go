// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"reflect"
	"regexp"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// Verdict is what one type ends up as.
type Verdict string

const (
	// VerdictRowed: a row builds the identity from configuration.
	VerdictRowed Verdict = "rowed"

	// VerdictServerAssigned: a row says the provider mints the identity,
	// and the type carries labels, so marker discovery finds it.
	VerdictServerAssigned Verdict = "server-assigned"

	// VerdictMarkerless: the provider mints the identity and the type has
	// no labels, so there is nowhere to write a marker. Vetoed, the way
	// identity.MarkerlessTypes vetoes AWS's.
	VerdictMarkerless Verdict = "markerless"

	// VerdictUnrowed: the source states no identity a row can be built
	// from. The type gets what an unrowed AWS type gets: the
	// identity-schema route, or a refusal.
	VerdictUnrowed Verdict = "unrowed"
)

// Decision is one type's verdict and the row behind it, if any.
type Decision struct {
	Type      string
	Providers []string
	Labels    bool
	Verdict   Verdict
	Row       *identity.TypeIdentity
	// Template is the id template the row was built from, after any
	// derived placeholder is expanded.
	Template string
	// Reason says why an unrowed type has no row, or why a server-assigned
	// or markerless one is minted.
	Reason string
	// NoSource is set when the source states no id format at all, the GCP
	// shape of AWS's "no import evidence".
	NoSource bool
	// Parents are the placeholders of a rowed type's template that name a
	// containing resource rather than this one or its scope.
	Parents []string
	// Scoped is true when every configuration-supplied component is
	// project, region or zone: one per project or region.
	Scoped bool
}

// cloudPlaceholders are the three variables ReplaceVars never reads from the
// resource alone: tpgresource.GetProject, GetRegion and GetZone take the
// resource's own argument when it has one and the provider's otherwise.
var cloudPlaceholders = map[string]identity.CloudValue{
	"project": identity.CloudProject,
	"region":  identity.CloudRegion,
	"zone":    identity.CloudZone,
}

// scopePlaceholders name where a resource lives rather than a resource it
// lives under, for the parent-derived split.
var scopePlaceholders = map[string]bool{
	"project": true, "region": true, "zone": true, "location": true,
	"organization": true, "org_id": true, "folder": true, "folder_id": true,
	"billing_account": true, "parent": true,
}

// LabelSurface is the google-labels shape: a settable `labels` map with the
// provider's computed `effective_labels` beside it, in every provider that
// serves the type, and a labels map the type keeps rather than copies onto
// what it creates. The last is [markers.GoogleLabelSurface]'s own
// exclusion, recorded by the extract as [TypeFacts.LabelsPropagate]:
// without it the two instance templates read as marker-carried here while
// the stamp refuses them (GitHub issue #2145).
func LabelSurface(tfs []TypeFacts) bool {
	if len(tfs) == 0 {
		return false
	}
	for _, tf := range tfs {
		l := tf.Attrs["labels"]
		if (l != "O" && l != "OC") || tf.Attrs["effective_labels"] != "C" || tf.LabelsPropagate {
			return false
		}
	}
	return true
}

// Decide derives one type's verdict from what each provider's source says
// and what each provider's schema serves.
func Decide(typeName string, sources map[string]Source, facts map[string]TypeFacts) Decision {
	d := Decision{Type: typeName}
	var tfs []TypeFacts
	for _, p := range sortedKeys(facts) {
		d.Providers = append(d.Providers, p)
		tfs = append(tfs, facts[p])
	}
	d.Labels = LabelSurface(tfs)

	unrowed := func(noSource bool, format string, args ...any) Decision {
		d.Verdict, d.Row, d.NoSource = VerdictUnrowed, nil, noSource
		d.Reason = fmt.Sprintf(format, args...)
		return d
	}

	if len(sources) != len(facts) {
		return unrowed(true, "%s's provider source registers no resource by this name", strings.Join(missingProviders(sources, facts), " and "))
	}
	var src Source
	for i, p := range sortedProviders(sources) {
		s := sources[p]
		if i == 0 {
			src = s
			continue
		}
		if !sameIdentitySource(src, s) {
			return unrowed(false, "google and google-beta state different identities for this type, so one row cannot serve both")
		}
	}

	switch src.Kind {
	case "resource":
		return decideResource(d, src, tfs, unrowed)
	case "iam_member", "iam_binding", "iam_policy", "iam_audit_config":
		return decideIAM(d, src, tfs, unrowed)
	}
	return unrowed(true, "the source registers this type in a shape this generator does not read")
}

type unrowedFunc func(noSource bool, format string, args ...any) Decision

func decideResource(d Decision, src Source, tfs []TypeFacts, unrowed unrowedFunc) Decision {
	if src.IDFormat == "" {
		return unrowed(true, "the provider source sets this type's id some other way than an id format template")
	}
	if src.IDFromResponse != "" {
		// The id format found is the importer's; what a create leaves as
		// the id is whatever the API answered.
		reason := fmt.Sprintf("the provider's create sets the id from the API response's %q, which only the API knows; the template %s is its importer's.", src.IDFromResponse, src.IDFormat)
		if !d.Labels {
			d.Verdict, d.Reason = VerdictMarkerless, reason
			return d
		}
		return unrowed(false, "%s", reason)
	}

	// A computed placeholder the provider's own CustomizeDiff derives from
	// configuration is expanded in place, and every piece of the expansion
	// supplies the identity attribute it was derived into, the way an SNS
	// topic's arn is concatenated out of its pieces.
	var toks []token
	tmpl := ""
	for _, tok := range tokenize(src.IDFormat) {
		derived, ok := src.Derived[tok.name]
		if tok.name == "" || !ok || attrFlags(tfs, tok.name) != "C" {
			toks = append(toks, tok)
			tmpl += tok.text()
			continue
		}
		for _, sub := range tokenize(derived) {
			sub.into = tok.name
			toks = append(toks, sub)
			tmpl += sub.text()
		}
	}
	d.Template = tmpl

	identitySchema := identityNames(tfs)
	idAttr := func(name string) string {
		if identitySchema == nil || identitySchema[name] {
			return identity.SameNameIdentity
		}
		return ""
	}
	intoAttr := func(tok token) string {
		if identitySchema == nil || identitySchema[tok.into] {
			return tok.into
		}
		return ""
	}

	var comps []identity.Component
	var serverAssigned []string
	var configNamed []string
	last := ""
	for _, tok := range toks {
		if tok.literal != "" {
			lit := ""
			if tok.into != "" {
				lit = intoAttr(tok)
			}
			comps = appendLiteral(comps, tok.literal, lit)
			continue
		}
		name := tok.name
		if tok.escaped {
			return unrowed(false, "the id format path-escapes {{%%%s}}, which a row cannot reproduce", name)
		}
		last = name
		flag := attrFlags(tfs, name)
		comp := identity.Component{IdentityAttr: idAttr(name)}
		if tok.into != "" {
			comp.IdentityAttr = intoAttr(tok)
		}
		if src.IDShortened {
			// ReplaceVarsForId substitutes the last path segment of every
			// argument it reads, and trims "projects/", "regions/" and
			// "zones/" off the scope values; the row substitutes the whole
			// value (#2144).
			comp.LongForm = identity.LongFormLastSegment
		}
		if cloud, ok := cloudPlaceholders[name]; ok && flag != "R" {
			if flag != "" {
				comp.Attrs = []string{name}
			}
			comp.Cloud = cloud
			comps = append(comps, comp)
			continue
		}
		switch flag {
		case "":
			return unrowed(false, "the id format names {{%s}}, which is no attribute of the schema", name)
		case "R":
			comp.Attrs = []string{name}
		case "OC":
			if name == "location" {
				return unrowed(false, "the id format names {{location}}, which the provider fills in by a rule of this type's own when it is unset")
			}
			comp.Attrs = []string{name}
			comp.ServerAssignedIfAbsent = true
		case "O":
			// An optional argument the schema does not mark computed is
			// never filled in by the API: ReplaceVars reads it from
			// configuration or renders it empty. Written, it is known
			// before apply exactly as a required one is. Unwritten and
			// with no declared default, the component has no value and
			// resolution refuses that one instance by name, the way it
			// refuses a required argument left out; the type keeps its row.
			comp.Attrs = []string{name}
			if def, ok := src.Defaults[name]; ok {
				comp.Default = def
			}
		case "C":
			serverAssigned = append(serverAssigned, name)
			continue
		default:
			return unrowed(false, "the id format names {{%s}}, which is %s in one provider and something else in the other", name, flag)
		}
		configNamed = append(configNamed, name)
		comps = append(comps, comp)
	}

	// The importer is asked only of an identity configuration names: a
	// minted one is never imported by a string built here.
	if len(serverAssigned) == 0 {
		switch {
		case src.ImportIDFormat == "":
			return unrowed(true, "the provider source has no ParseImportId importer for this type")
		case src.IDFormat != src.ImportIDFormat:
			return unrowed(false, "the id format %q and the importer's %q differ", src.IDFormat, src.ImportIDFormat)
		}
	}

	if len(serverAssigned) > 0 {
		d.Reason = fmt.Sprintf("the provider's id format %s embeds %s, which the API assigns at create time (the schema marks it computed only); no argument reconstructs it.",
			tmpl, joinQuoted(serverAssigned))
		if !d.Labels {
			d.Verdict = VerdictMarkerless
			return d
		}
		d.Verdict = VerdictServerAssigned
		d.Row = &identity.TypeIdentity{
			Type:           d.Type,
			ServerAssigned: true,
			Reason:         d.Reason,
			NonAWSProvider: true,
			ImportSyntax:   tmpl,
			IdentityAttrs:  []string{"id"},
		}
		return d
	}

	identityAttrs := []string{"id"}
	if len(comps) == 1 && len(comps[0].Attrs) == 1 && comps[0].Cloud == identity.CloudNone && !comps[0].ServerAssignedIfAbsent && comps[0].Default == "" {
		identityAttrs = append(identityAttrs, comps[0].Attrs[0])
	}
	sort.Strings(identityAttrs)
	d.Verdict = VerdictRowed
	d.Row = &identity.TypeIdentity{
		Type:           d.Type,
		NonAWSProvider: true,
		Components:     comps,
		ImportSyntax:   tmpl,
		IdentityAttrs:  identityAttrs,
	}
	for _, n := range configNamed {
		if n != last && !scopePlaceholders[n] {
			d.Parents = append(d.Parents, n)
		}
	}
	d.Scoped = true
	for _, n := range configNamed {
		if _, cloud := cloudPlaceholders[n]; !cloud {
			d.Scoped = false
		}
	}
	return d
}

func decideIAM(d Decision, src Source, tfs []TypeFacts, unrowed unrowedFunc) Decision {
	if src.IAMParent == "" {
		return unrowed(true, "the IAM type's IdParseFunc reads its parent id in a shape this generator does not read")
	}
	if src.IAMGrammar == "" {
		return unrowed(true, "the IAM framework states no import grammar for this kind")
	}
	identitySchema := identityNames(tfs)
	idAttr := func(name string) string {
		if identitySchema == nil || identitySchema[name] {
			return identity.SameNameIdentity
		}
		return ""
	}

	// The parent template the row spells. A computed-name helper builds
	// the parent from the provider's project and the argument whenever the
	// argument is not already a full name, so its format is the template;
	// the IdParseFunc's "{{subscription}}" is only what an import sets.
	parent := src.IAMParent
	computed := src.IAMComputedFormat != "" && src.IAMParent == "{{"+src.IAMQualified+"}}"
	if computed {
		parent = src.IAMComputedFormat
	}
	// The qualifier list is carried only where its first regex is the
	// parent template itself, the shape every GetResourceId rebuilds; a
	// list that disagrees leaves the component marked with none, and a
	// value that needs it refuses by name.
	var qualifiers []string
	if len(src.IAMQualifiers) > 0 {
		if tmpl, ok := regexToTemplate(src.IAMQualifiers[0]); ok && tmpl == parent && compileAll(src.IAMQualifiers) {
			qualifiers = src.IAMQualifiers
		}
	}

	var comps []identity.Component
	for _, tok := range tokenize(parent) {
		if tok.literal != "" {
			comps = appendLiteral(comps, tok.literal, "")
			continue
		}
		flag := attrFlags(tfs, tok.name)
		comp := identity.Component{IdentityAttr: idAttr(tok.name)}
		if tok.name == src.IAMQualified {
			// The updater parses this argument and rebuilds the
			// canonical parent from it (#2144): GetImportIdQualifiers on
			// a generated updater, a computed-name helper on a
			// hand-written one.
			comp.LongForm = identity.LongFormIAMParent
			comp.Qualifiers = qualifiers
			if computed {
				comp.LongForm = identity.LongFormComputedName
				comp.Qualifiers = []string{src.IAMComputedPattern}
			}
		}
		if cloud, ok := iamCloud(src, tfs, tok.name); ok && flag != "R" {
			if flag != "" {
				comp.Attrs = []string{tok.name}
			}
			comp.Cloud = cloud
			comps = append(comps, comp)
			continue
		}
		if flag != "R" {
			return unrowed(false, "the IAM parent id %s names {{%s}}, which the schema does not require", parent, tok.name)
		}
		comp.Attrs = []string{tok.name}
		comps = append(comps, comp)
	}

	words := strings.Fields(src.IAMGrammar)
	if len(words) == 0 || words[0] != "resource_name" {
		return unrowed(true, "the IAM grammar %q does not start with the parent id", src.IAMGrammar)
	}
	for _, w := range words[1:] {
		switch {
		case w == "[condition_title]":
			bf, ok := blockLeaf(tfs, "condition", "title")
			if !ok {
				return unrowed(false, "the IAM grammar ends in a condition title, and the schema has no single condition block with a title")
			}
			_ = bf
			comps = append(comps, identity.Component{
				Literal:      " ",
				Attrs:        []string{"title"},
				Block:        "condition",
				OmitIfAbsent: true,
				IdentityAttr: condIdentityAttr(identitySchema),
			})
		case strings.HasPrefix(w, "["):
			return unrowed(true, "the IAM grammar %q has an optional part this generator does not read", src.IAMGrammar)
		default:
			if attrFlags(tfs, w) != "R" {
				return unrowed(false, "the IAM grammar names %s, which the schema does not require", w)
			}
			comps = appendLiteral(comps, " ", "")
			comps = append(comps, identity.Component{Attrs: []string{w}, IdentityAttr: idAttr(w)})
		}
	}

	d.Template = parent + strings.TrimPrefix(src.IAMGrammar, "resource_name")
	d.Verdict = VerdictRowed
	d.Row = &identity.TypeIdentity{
		Type:           d.Type,
		NonAWSProvider: true,
		Components:     comps,
		ImportSyntax:   d.Template,
	}
	d.Parents = []string{iamParent(d.Type)}
	return d
}

// iamCloud is the provider-side default an IAM type's parent placeholder
// falls back to, when its file reads that placeholder through
// tpgresource.GetProject, GetRegion, GetZone or GetLocation. GetLocation
// reads the resource's own region and zone before the provider's, so it
// stands for the provider's region or zone only on a type with neither.
func iamCloud(src Source, tfs []TypeFacts, name string) (identity.CloudValue, bool) {
	if !containsStr(src.IAMDefaults, name) {
		return identity.CloudNone, false
	}
	if name == "location" {
		if attrFlags(tfs, "region") != "" || attrFlags(tfs, "zone") != "" {
			return identity.CloudNone, false
		}
		return identity.CloudLocation, true
	}
	cloud, ok := cloudPlaceholders[name]
	return cloud, ok
}

func condIdentityAttr(identitySchema map[string]bool) string {
	if identitySchema != nil && identitySchema["condition_title"] {
		return "condition_title"
	}
	return ""
}

// appendLiteral adds a separator, merging it into a separator just before.
func appendLiteral(comps []identity.Component, lit, idAttr string) []identity.Component {
	if n := len(comps); n > 0 {
		prev := comps[n-1]
		if len(prev.Attrs) == 0 && prev.Cloud == identity.CloudNone && prev.IdentityAttr == idAttr {
			comps[n-1].Literal += lit
			return comps
		}
	}
	return append(comps, identity.Component{Literal: lit, IdentityAttr: idAttr})
}

type token struct {
	literal string
	name    string
	escaped bool
	// into is the computed attribute this token was expanded out of, or
	// empty.
	into string
}

func (t token) text() string {
	switch {
	case t.literal != "":
		return t.literal
	case t.escaped:
		return "{{%" + t.name + "}}"
	default:
		return "{{" + t.name + "}}"
	}
}

func tokenize(tmpl string) []token {
	var out []token
	pos := 0
	for _, m := range placeholderRe.FindAllStringSubmatchIndex(tmpl, -1) {
		if m[0] > pos {
			out = append(out, token{literal: tmpl[pos:m[0]]})
		}
		out = append(out, token{name: tmpl[m[4]:m[5]], escaped: m[3] > m[2]})
		pos = m[1]
	}
	if pos < len(tmpl) {
		out = append(out, token{literal: tmpl[pos:]})
	}
	return out
}

// attrFlags is an attribute's flags, the same in every provider serving the
// type, or "" when one of them lacks it, or "mixed" when they disagree.
func attrFlags(tfs []TypeFacts, name string) string {
	got := ""
	for i, tf := range tfs {
		f, ok := tf.Attrs[name]
		if !ok {
			return ""
		}
		if i == 0 {
			got = f
		} else if f != got {
			return "mixed"
		}
	}
	return got
}

func blockLeaf(tfs []TypeFacts, block, leaf string) (string, bool) {
	got := ""
	for _, tf := range tfs {
		b, ok := tf.Blocks[block]
		if !ok || b.MaxItems != 1 {
			return "", false
		}
		f, ok := b.Attrs[leaf]
		if !ok {
			return "", false
		}
		got = f
	}
	return got, len(tfs) > 0
}

// identityNames is the set of identity attributes every provider serving
// the type agrees on, or nil when none of them serves an identity schema.
func identityNames(tfs []TypeFacts) map[string]bool {
	var out map[string]bool
	for _, tf := range tfs {
		if tf.Identity == nil {
			continue
		}
		if out == nil {
			out = map[string]bool{}
			for n := range tf.Identity {
				out[n] = true
			}
			continue
		}
		for n := range out {
			if _, ok := tf.Identity[n]; !ok {
				delete(out, n)
			}
		}
	}
	return out
}

func sameIdentitySource(a, b Source) bool {
	a.File, b.File = "", ""
	a.Generation, b.Generation = "", ""
	a.ImportFormats, b.ImportFormats = nil, nil
	a.APIService, b.APIService = "", ""
	a.CAINameFormats, b.CAINameFormats = nil, nil
	a.DocImportFormats, b.DocImportFormats = nil, nil
	return reflect.DeepEqual(a, b)
}

func missingProviders(sources map[string]Source, facts map[string]TypeFacts) []string {
	var out []string
	for _, p := range sortedKeys(facts) {
		if _, ok := sources[p]; !ok {
			out = append(out, p)
		}
	}
	return out
}

func joinQuoted(names []string) string {
	q := make([]string, len(names))
	for i, n := range names {
		q[i] = "{{" + n + "}}"
	}
	return strings.Join(q, " and ")
}

// compileAll reports whether every pattern compiles as Go's regexp, the
// package the provider's own GetImportIdQualifiers compiles it with.
func compileAll(patterns []string) bool {
	for _, p := range patterns {
		if _, err := regexp.Compile(p); err != nil {
			return false
		}
	}
	return true
}
