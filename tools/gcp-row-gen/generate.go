// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/format"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// Survey is live/gcp-survey.json: every hashicorp/google and google-beta
// resource type and what this generator decided for it. tools/readiness-gen
// tiers each type from these facts, and renders how each type comes back
// from the same facts (#2134 item 12): the survey carries no classification
// of its own.
type Survey struct {
	GeneratedBy string       `json:"generated_by"`
	Providers   []string     `json:"providers"`
	Version     string       `json:"version"`
	Counts      SurveyCounts `json:"counts"`
	Types       []SurveyRow  `json:"types"`
}

// SurveyCounts are the survey's totals, over the union of both providers'
// types, and over hashicorp/google's own.
type SurveyCounts struct {
	Types          int `json:"types"`
	Rowed          int `json:"rowed"`
	ServerAssigned int `json:"server_assigned"`
	Markerless     int `json:"markerless"`
	SchemaRouted   int `json:"schema_routed"`
	Refused        int `json:"refused"`
	Google         struct {
		Types        int `json:"types"`
		Rowed        int `json:"rowed"`
		Markerless   int `json:"markerless"`
		SchemaRouted int `json:"schema_routed"`
		Refused      int `json:"refused"`
	} `json:"google"`
}

// SurveyRow is one type.
type SurveyRow struct {
	Type      string   `json:"type"`
	Providers []string `json:"providers"`
	// Labels is the google-labels marker surface.
	Labels bool `json:"labels"`
	// Verdict is rowed, server-assigned, markerless or unrowed.
	Verdict Verdict `json:"verdict"`
	// SchemaRoute is set on an unrowed type the identity-schema route
	// (identity.SynthesizeTypeIdentity) admits.
	SchemaRoute bool `json:"schema_route,omitempty"`
	// NoSource is set on an unrowed type whose source states no id format.
	NoSource bool `json:"no_source,omitempty"`
	// Parents are the template placeholders that name a containing
	// resource rather than this one or its scope; for an IAM type, the
	// resource its policy is on. Scoped is set on a rowed type whose every
	// configuration-supplied component is project, region or zone.
	Parents  []string `json:"parents,omitempty"`
	Scoped   bool     `json:"scoped,omitempty"`
	Template string   `json:"template,omitempty"`
	Reason   string   `json:"reason,omitempty"`
}

// Output is everything one derivation writes.
type Output struct {
	Decisions []Decision
	Survey    Survey
	Table     []byte
	SurveyRaw []byte

	// Assets, Composites and ImportCounts are the import-ID tables
	// (importids.go), rendered as ImportIDs.
	Assets       []AssetDecision
	Composites   []CompositeDecision
	ImportCounts ImportIDCounts
	ImportIDs    []byte
}

// Files maps each output to its repository-relative path.
func (o Output) Files() map[string][]byte {
	return map[string][]byte{TableGoRel: o.Table, SurveyJSONRel: o.SurveyRaw, ImportIDsGoRel: o.ImportIDs}
}

// Generate is the whole derivation from the three evidence artifacts.
func Generate(ev Evidence, facts Facts, names AssetNames) (Output, error) {
	byType := map[string]map[string]TypeFacts{}
	for p, pf := range facts.Providers {
		for t, tf := range pf.Types {
			if byType[t] == nil {
				byType[t] = map[string]TypeFacts{}
			}
			byType[t][p] = tf
		}
	}
	schemas := map[string]map[string]providers.Schema{}
	for p, pf := range facts.Providers {
		schemas[p] = providerSchemas(pf)
	}

	var out Output
	for _, t := range sortedKeys(byType) {
		out.Decisions = append(out.Decisions, Decide(t, ev.Types[t], byType[t]))
	}

	out.Survey = Survey{
		GeneratedBy: "go run ./tools/gcp-row-gen",
		Providers:   []string{"hashicorp/" + ProviderGoogle, "hashicorp/" + ProviderGoogleBeta},
		Version:     ProviderVersion,
	}
	c := &out.Survey.Counts
	for _, d := range out.Decisions {
		row := SurveyRow{
			Type:      d.Type,
			Providers: d.Providers,
			Labels:    d.Labels,
			Verdict:   d.Verdict,
			NoSource:  d.NoSource,
			Parents:   d.Parents,
			Scoped:    d.Scoped,
			Template:  d.Template,
			Reason:    d.Reason,
		}
		if d.Verdict == VerdictUnrowed {
			row.SchemaRoute = schemaRouted(d.Type, d.Providers, schemas)
		}
		if len(row.Parents) == 0 && isIAM(d.Type) {
			row.Parents = []string{iamParent(d.Type)}
		}
		out.Survey.Types = append(out.Survey.Types, row)

		c.Types++
		inGoogle := containsStr(d.Providers, ProviderGoogle)
		if inGoogle {
			c.Google.Types++
		}
		switch {
		case d.Verdict == VerdictRowed || d.Verdict == VerdictServerAssigned:
			c.Rowed++
			if d.Verdict == VerdictServerAssigned {
				c.ServerAssigned++
			}
			if inGoogle {
				c.Google.Rowed++
			}
		case d.Verdict == VerdictMarkerless:
			c.Markerless++
			c.Refused++
			if inGoogle {
				c.Google.Markerless++
				c.Google.Refused++
			}
		case row.SchemaRoute:
			c.SchemaRouted++
			if inGoogle {
				c.Google.SchemaRouted++
			}
		default:
			c.Refused++
			if inGoogle {
				c.Google.Refused++
			}
		}
	}

	raw, err := json.MarshalIndent(out.Survey, "", "  ")
	if err != nil {
		return Output{}, err
	}
	out.SurveyRaw = append(raw, '\n')

	table, err := renderTable(out.Decisions)
	if err != nil {
		return Output{}, err
	}
	out.Table = table

	out.Assets, out.Composites, out.ImportCounts, err = deriveImportIDs(ev, names, byType, out.Decisions)
	if err != nil {
		return Output{}, err
	}
	importIDs, err := renderImportIDs(out.Assets, out.Composites, out.ImportCounts, names)
	if err != nil {
		return Output{}, err
	}
	out.ImportIDs = importIDs
	return out, nil
}

func isIAM(t string) bool {
	for _, k := range iamKinds {
		if strings.HasSuffix(t, k.suffix) {
			return true
		}
	}
	return false
}

// iamParent is the resource type an IAM type's policy is on:
// google_compute_instance for google_compute_instance_iam_member.
func iamParent(t string) string {
	for _, k := range iamKinds {
		if p, ok := strings.CutSuffix(t, k.suffix); ok {
			return p
		}
	}
	return t
}

// schemaRouted is whether identity.SynthesizeTypeIdentity admits the type
// over every serving provider's schema, which is what a run with no row for
// the type reaches for.
func schemaRouted(t string, provs []string, schemas map[string]map[string]providers.Schema) bool {
	if len(provs) == 0 {
		return false
	}
	for _, p := range provs {
		if _, ok := identity.SynthesizeTypeIdentity(t, schemas[p], nil); !ok {
			return false
		}
	}
	return true
}

// providerSchemas rebuilds one provider's schemas from its facts, far
// enough for identity.SynthesizeTypeIdentity: attribute flags, nested
// attributes and blocks, and the identity schema. Every attribute is typed
// as a string; nothing the route reads depends on the type.
func providerSchemas(pf ProviderFacts) map[string]providers.Schema {
	out := make(map[string]providers.Schema, len(pf.Types))
	for t, tf := range pf.Types {
		b := &configschema.Block{Attributes: map[string]*configschema.Attribute{}, BlockTypes: map[string]*configschema.NestedBlock{}}
		for name, f := range tf.Attrs {
			a := attrFromFlags(f)
			if leaves, ok := tf.Nested[name]; ok {
				obj := &configschema.Object{Attributes: map[string]*configschema.Attribute{}, Nesting: configschema.NestingSingle}
				for ln, lf := range leaves {
					obj.Attributes[ln] = attrFromFlags(lf)
				}
				a.Type = cty.NilType
				a.NestedType = obj
			}
			b.Attributes[name] = a
		}
		for name, bf := range tf.Blocks {
			nb := &configschema.NestedBlock{Nesting: configschema.NestingList, MaxItems: bf.MaxItems}
			nb.Attributes = map[string]*configschema.Attribute{}
			for ln, lf := range bf.Attrs {
				nb.Attributes[ln] = attrFromFlags(lf)
			}
			b.BlockTypes[name] = nb
		}
		s := providers.Schema{Block: b}
		if tf.Identity != nil {
			obj := &configschema.Object{Attributes: map[string]*configschema.Attribute{}, Nesting: configschema.NestingSingle}
			for name, f := range tf.Identity {
				obj.Attributes[name] = &configschema.Attribute{Type: cty.String, Required: f == "R", Optional: f != "R"}
			}
			s.IdentitySchema = obj
		}
		out[t] = s
	}
	return out
}

func attrFromFlags(f string) *configschema.Attribute {
	return &configschema.Attribute{
		Type:     cty.String,
		Required: f == "R",
		Optional: f == "O" || f == "OC",
		Computed: f == "OC" || f == "C",
	}
}

func containsStr(ss []string, s string) bool {
	for _, v := range ss {
		if v == s {
			return true
		}
	}
	return false
}

// GoogleMarkerlessReason is the generated constant's text.
const googleMarkerlessReason = "the provider mints this type's identity and the type has no labels argument, so every instance would need marker discovery to be found again and there is nowhere to write the marker"

func renderTable(decisions []Decision) ([]byte, error) {
	var b bytes.Buffer
	b.WriteString(`// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Code generated by tools/gcp-row-gen. DO NOT EDIT.

package identity

`)
	fmt.Fprintf(&b, `// GoogleTable is the identity table for hashicorp/google and
// hashicorp/google-beta %s resource types, keyed by type name. One row
// serves both providers: a type the two serve with different identities
// has no row.
//
// Every row is derived by tools/gcp-row-gen from the provider's own source
// at the v%s tag (live/gcp-identity-evidence.json) and checked against both
// providers' schemas (live/gcp-schema-facts.json). A type with no row gets
// what an unrowed AWS type gets: the identity-schema route, or a refusal.
// Corrections belong in the generator, never here.
var GoogleTable = map[string]TypeIdentity{
`, ProviderVersion, ProviderVersion)
	var markerless []string
	for _, d := range decisions {
		if d.Verdict == VerdictMarkerless {
			markerless = append(markerless, d.Type)
		}
		if d.Row == nil {
			continue
		}
		fmt.Fprintf(&b, "\t%q: %s,\n", d.Type, renderRow(*d.Row))
	}
	b.WriteString("}\n\n")
	fmt.Fprintf(&b, `// GoogleMarkerlessReason is why every type in [GoogleMarkerlessTypes] is
// outside [GoogleTable], the hashicorp/google wording of [MarkerlessReason].
const GoogleMarkerlessReason = %q

// GoogleMarkerlessTypes is every hashicorp/google and google-beta resource
// type tools/gcp-row-gen refuses to admit because the provider mints its
// identity and it carries no labels argument - see
// [GoogleMarkerlessReason]. A veto derived by rule, as [MarkerlessTypes]
// is for AWS.
var GoogleMarkerlessTypes = map[string]struct{}{
`, googleMarkerlessReason)
	sort.Strings(markerless)
	for _, t := range markerless {
		fmt.Fprintf(&b, "\t%q: {},\n", t)
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}

func renderRow(r identity.TypeIdentity) string {
	var f []string
	f = append(f, fmt.Sprintf("Type: %q", r.Type))
	if r.ServerAssigned {
		f = append(f, "ServerAssigned: true", fmt.Sprintf("Reason: %q", r.Reason))
	}
	if r.NonAWSProvider {
		f = append(f, "NonAWSProvider: true")
	}
	if len(r.Components) > 0 {
		var cs []string
		for _, c := range r.Components {
			cs = append(cs, renderComponent(c))
		}
		f = append(f, "Components: []Component{"+strings.Join(cs, ", ")+"}")
	}
	f = append(f, fmt.Sprintf("ImportSyntax: %q", r.ImportSyntax))
	if r.IdentityAttrs != nil {
		f = append(f, "IdentityAttrs: "+renderStrings(r.IdentityAttrs))
	}
	return "{" + strings.Join(f, ", ") + "}"
}

func renderComponent(c identity.Component) string {
	var f []string
	if c.Literal != "" {
		f = append(f, fmt.Sprintf("Literal: %q", c.Literal))
	}
	if len(c.Attrs) > 0 {
		f = append(f, "Attrs: "+renderStrings(c.Attrs))
	}
	if c.Block != "" {
		f = append(f, fmt.Sprintf("Block: %q", c.Block))
	}
	if c.OmitIfAbsent {
		f = append(f, "OmitIfAbsent: true")
	}
	if c.Default != "" {
		f = append(f, fmt.Sprintf("Default: %q", c.Default))
	}
	if c.ServerAssignedIfAbsent {
		f = append(f, "ServerAssignedIfAbsent: true")
	}
	if c.Cloud != identity.CloudNone {
		f = append(f, fmt.Sprintf("Cloud: %q", string(c.Cloud)))
	}
	if c.IdentityAttr != "" {
		f = append(f, fmt.Sprintf("IdentityAttr: %q", c.IdentityAttr))
	}
	if c.LongForm != "" {
		f = append(f, fmt.Sprintf("LongForm: %q", string(c.LongForm)))
	}
	return "{" + strings.Join(f, ", ") + "}"
}

func renderStrings(ss []string) string {
	q := make([]string, len(ss))
	for i, s := range ss {
		q[i] = fmt.Sprintf("%q", s)
	}
	return "[]string{" + strings.Join(q, ", ") + "}"
}
