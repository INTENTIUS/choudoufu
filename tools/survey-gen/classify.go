// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// The path vocabulary is SURVEY.md's, verbatim: its per-type table promises
// that nothing outside these tokens appears in the Path column, so the
// generated artifact speaks the same tokens.
const (
	pathClientNamed    = "client-named"
	pathMarker         = "marker"
	pathParentDerived  = "parent-derived"
	pathListContent    = "list + content match"
	pathAccountDerived = "account-derived"
	pathOps            = "moves to Ops"
)

// opsExcluded is the one hand-written input to the classifier, per issue
// #25's own design: "what legitimately stays hand-written: the
// excluded-by-rule set ... credentials and waiters need the Ops judgment."
// No schema says a secret is unreadable after create or that a resource is
// a waiter, so these three carry their reason here instead of a derivation.
var opsExcluded = map[string]string{
	"aws_iam_access_key":                "credential: the secret half is unreadable after create, and an external holder makes set semantics inapplicable",
	"aws_secretsmanager_secret_version": "credential: secret_id plus a server-assigned version UUID, the secret unreadable after create",
	"aws_acm_certificate_validation":    "waiter: records only that DNS validation finished; waiting belongs to the lifecycle layer",
}

// Survey is the committed artifact: live/survey.json.
type Survey struct {
	// Provider and ProviderVersion pin the release every row was derived
	// from. No timestamp on purpose: regeneration against the same release
	// must be byte-identical.
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version"`

	// GeneratedBy names the tool so a reader of the JSON alone knows where
	// rows come from and how to refresh them.
	GeneratedBy string `json:"generated_by"`

	// Counts are the roster-wide raw-signal totals, the figures SURVEY.md's
	// "Raw signals" section records by hand.
	Counts Counts `json:"counts"`

	// Types has one row per surveyed type, sorted by type name.
	Types []Row `json:"types"`
}

// Counts are the raw-signal totals over the surveyed roster.
type Counts struct {
	Types          int `json:"types"`
	Taggable       int `json:"taggable"`
	ListResource   int `json:"list_resource"`
	IdentitySchema int `json:"identity_schema"`
}

// Row is one surveyed type.
type Row struct {
	// Type is the resource type name; Path is its mechanically classified
	// admission path, one of SURVEY.md's five Path tokens.
	Type string `json:"type"`
	Path string `json:"path"`

	// Signals are the three raw signals SURVEY.md's Method step 4 records.
	Signals Signals `json:"signals"`

	// Identity is the identity attribute composition from the provider's
	// resource identity schema, absent when the provider ships none.
	Identity *IdentityAttrs `json:"identity,omitempty"`

	// Admission is what would let the fork's identity table carry this type
	// with no hand-written row: "schema" when the provider's schemas settle
	// that the configuration names the resource, "needs-config-signal" when
	// they leave that to whether a configuration sets the identity
	// attributes above (the Optional+Computed cohort, which is most of the
	// name-prefix idiom's exceptions below). Absent when neither admits it.
	//
	// This is the column the admission table shrinks along, so it is worth
	// separating from Path: Path says how a live resource is recovered,
	// Admission says who has to write the row that recovers it. See
	// internal/live/identity's Report.
	Admission string `json:"admission,omitempty"`

	// Evidence is one sentence saying what the classifier saw.
	Evidence string `json:"evidence"`
}

// Signals are the per-type raw signals, read straight off the provider's
// GetProviderSchema response.
type Signals struct {
	// Taggable: a top-level settable tags map, the same predicate the
	// marker path applies (internal/live/stamp's taggable).
	Taggable bool `json:"taggable"`

	// ListResource: the provider serves a native list resource for the
	// type (GetProviderSchemaResponse.ListResourceTypes).
	ListResource bool `json:"list_resource"`

	// IdentitySchema: the provider ships a resource identity schema for
	// the type.
	IdentitySchema bool `json:"identity_schema"`
}

// IdentityAttrs is the identity schema's attribute composition.
type IdentityAttrs struct {
	// RequiredForImport are the attributes the provider needs to import
	// the resource; OptionalForImport the ones it can fill in itself
	// (account_id and region, in the AWS provider). Both sorted.
	RequiredForImport []string `json:"required_for_import"`
	OptionalForImport []string `json:"optional_for_import,omitempty"`
}

// buildSurvey derives one row per roster type from the provider's schemas.
func buildSurvey(schema providers.GetProviderSchemaResponse, roster []string) Survey {
	// The strict client-named judgment is identity.Derivable's, not this
	// tool's: it is the one classifier that already knows the
	// Optional+Computed trap (aws_s3_bucket.bucket and aws_vpc.id are the
	// same shape in a legacy-SDK schema and opposite answers; see
	// internal/live/identity/doc.go).
	derivable := map[string]identity.DerivableType{}
	for _, d := range identity.Derivable(schema.ResourceTypes) {
		derivable[d.Type] = d
	}

	// The derivability report over the same schemas, with no configuration
	// to read: this tool surveys a provider release, not an estate, so the
	// cohort the schemas cannot settle comes back "needs-config-signal"
	// naming the arguments a configuration would have to set. That is the
	// useful thing to record here - it says what each unadmitted type is
	// waiting for - and it is the one column a later batch can act on
	// without reading anything by hand.
	report := identity.Report(schema.ResourceTypes, nil)

	// Sorted type names for the deterministic parent-reference scan.
	allTypes := make([]string, 0, len(schema.ResourceTypes))
	for name := range schema.ResourceTypes {
		allTypes = append(allTypes, name)
	}
	sort.Strings(allTypes)

	s := Survey{
		Provider:        providerSource,
		ProviderVersion: providerVersion,
		GeneratedBy:     "tools/survey-gen (go run ./tools/survey-gen)",
	}

	sorted := append([]string(nil), roster...)
	sort.Strings(sorted)
	for _, typeName := range sorted {
		row := classify(typeName, schema, derivable, allTypes)
		if c, ok := report.Admits(typeName); ok {
			row.Admission = string(c.Admits)
		}
		s.Counts.Types++
		if row.Signals.Taggable {
			s.Counts.Taggable++
		}
		if row.Signals.ListResource {
			s.Counts.ListResource++
		}
		if row.Signals.IdentitySchema {
			s.Counts.IdentitySchema++
		}
		s.Types = append(s.Types, row)
	}
	return s
}

// allResourceTypeNames is every resource type the provider's schemas carry,
// unsorted (buildSurvey sorts its roster argument itself). This is the -all
// flag's roster: issue #41's whole point is that buildSurvey already
// classifies provider-wide once given every type name instead of
// SURVEY.md's curated set, so this is the only new roster the -all mode
// needs.
func allResourceTypeNames(schema providers.GetProviderSchemaResponse) []string {
	out := make([]string, 0, len(schema.ResourceTypes))
	for name := range schema.ResourceTypes {
		out = append(out, name)
	}
	return out
}

// classify derives one type's row: the raw signals, the identity
// composition, and the admission path per SURVEY.md's Method section,
// strongest path first (client-named, marker, parent-derived, list plus
// content match; a type failing all four leaves the resource model).
//
// The mechanical rules, in the order applied:
//
//  1. The hand-curated Ops exclusion (credentials and waiters) wins
//     outright; no schema carries that judgment.
//  2. The fork's identity table builds the identity from configuration plus
//     the run's account or region, which is account-derived. Also not a
//     schema judgment - see cloudValuesOf.
//  3. identity.Derivable proves the identity fully client-assigned. Within
//     that set, a required identity attribute that names another managed
//     type's identity (an *_id or *_arn whose base names a resource type)
//     makes the path parent-derived; otherwise client-named.
//  4. An identity schema the strict rule cannot prove client-assigned falls
//     through to the discovery paths: marker when the type is taggable,
//     list plus content match when a native list resource exists, moves to
//     Ops when neither.
//  5. No identity schema at all: the same discovery fallback, with the
//     evidence saying the identity side is unreadable from schemas.
func classify(typeName string, schema providers.GetProviderSchemaResponse, derivable map[string]identity.DerivableType, allTypes []string) Row {
	rs := schema.ResourceTypes[typeName]
	_, hasList := schema.ListResourceTypes[typeName]

	row := Row{
		Type: typeName,
		Signals: Signals{
			Taggable:       taggable(rs.Block),
			ListResource:   hasList,
			IdentitySchema: rs.IdentitySchema != nil,
		},
	}
	if rs.IdentitySchema != nil {
		required, optional := identityAttrNames(rs.IdentitySchema)
		row.Identity = &IdentityAttrs{RequiredForImport: required, OptionalForImport: optional}
	}

	if rs.Block == nil {
		row.Path = pathOps
		row.Evidence = "the provider serves no schema for this type"
		return row
	}

	if reason, ok := opsExcluded[typeName]; ok {
		row.Path = pathOps
		row.Evidence = "excluded by hand per SURVEY.md's rule - " + reason
		return row
	}

	// The account-derived path, and the one place this classifier reads the
	// fork's identity table rather than the provider's schemas. It has to:
	// the schemas cannot tell an arn or url that wraps a client-chosen name
	// (an SQS queue, an SNS topic) from one that carries a server-generated
	// component (a Secrets Manager secret's six-character suffix), and the
	// difference is exactly whether a template built from the account and
	// the region reconstructs the identity. That judgment is asserted, once,
	// in internal/live/identity's table, as a component naming a cloud
	// value - so this reads the assertion instead of guessing at it, the
	// same way the client-named judgment defers to identity.Derivable.
	if cloud := cloudValuesOf(typeName); len(cloud) > 0 {
		row.Path = pathAccountDerived
		row.Evidence = fmt.Sprintf(
			"the identity table builds this type's import identity from configuration plus the run's %s",
			strings.Join(cloud, " and "))
		return row
	}

	if d, ok := derivable[typeName]; ok {
		var parents []string
		for _, attr := range d.IdentityAttrs {
			if parent, ok := parentRef(attr, typeName, allTypes); ok {
				parents = append(parents, fmt.Sprintf("%s (-> %s)", attr, parent))
			}
		}
		if len(parents) > 0 {
			row.Path = pathParentDerived
			row.Evidence = fmt.Sprintf("identity fully client-assigned (%s), composed over other types' identities: %s",
				strings.Join(d.IdentityAttrs, ", "), strings.Join(parents, ", "))
			return row
		}
		row.Path = pathClientNamed
		row.Evidence = fmt.Sprintf("every required-for-import identity attribute is a required argument: %s",
			strings.Join(d.IdentityAttrs, ", "))
		return row
	}

	// Not provably client-named. Say why before falling through to the
	// discovery paths, so the evidence names the identity-side reason.
	var identityNote string
	switch {
	case rs.IdentitySchema == nil:
		identityNote = "no identity schema in v" + providerVersion
	default:
		required := row.Identity.RequiredForImport
		if server := serverAssigned(required, rs.Block); len(server) > 0 {
			identityNote = fmt.Sprintf("identity requires server-assigned %s", strings.Join(server, ", "))
		} else if len(required) == 0 {
			identityNote = "identity schema requires nothing for import"
		} else {
			// The Optional+Computed cohort: the identity attributes are
			// settable arguments, but the schema cannot distinguish a
			// client-chosen name from a value the provider fills in
			// (identity/doc.go), so the strict rule refuses client-named.
			identityNote = fmt.Sprintf("identity attrs (%s) are settable but not required arguments, so client-naming is unprovable from the schema",
				strings.Join(required, ", "))
		}
	}

	switch {
	case row.Signals.Taggable:
		row.Path = pathMarker
		row.Evidence = identityNote + "; taggable, so recoverable by tag-filtered list"
	case hasList:
		row.Path = pathListContent
		row.Evidence = identityNote + "; untaggable, native list resource exists"
	default:
		row.Path = pathOps
		row.Evidence = identityNote + "; untaggable and no native list resource, so no admission path recovers it"
	}
	return row
}

// cloudValuesOf returns the cloud properties the fork's identity table
// substitutes into this type's import identity, in component order and
// deduplicated, or nothing when the table has no entry for the type or the
// entry names none. See the account-derived branch in classify.
func cloudValuesOf(typeName string) []string {
	entry, ok := identity.LookupType(typeName)
	if !ok {
		return nil
	}
	var out []string
	seen := map[identity.CloudValue]bool{}
	for _, c := range entry.Components {
		if c.Cloud == identity.CloudNone || seen[c.Cloud] {
			continue
		}
		seen[c.Cloud] = true
		out = append(out, string(c.Cloud))
	}
	return out
}

// serverAssigned returns the required-for-import identity attributes the
// resource schema shows as provider-minted: a bare id, arn or url (the
// names SURVEY.md's re-run calls server-assigned even where the legacy SDK
// marks them Optional+Computed), or an attribute configuration cannot set
// at all.
func serverAssigned(required []string, block *configschema.Block) []string {
	var out []string
	for _, name := range required {
		switch name {
		case "id", "arn", "url":
			out = append(out, name)
			continue
		}
		attr, ok := block.Attributes[name]
		if !ok || attr == nil || (!attr.Optional && !attr.Required) {
			out = append(out, name)
		}
	}
	return out
}

// parentRef reports whether an identity attribute names another managed
// type's identity: a *_id or *_arn whose base is a resource type
// (aws_<base>, or a type ending _<base>, the way target_group finds
// aws_lb_target_group). This is the "identity composed of other types' ids"
// rule from issue #25; a suffix like statement_id whose base names no
// resource type stays a client-chosen string.
//
// When several types end in the base, the shortest name wins (then
// alphabetical): zone_id should name aws_route53_zone, not
// aws_controltower_landing_zone. The named parent is evidence prose, not a
// wiring claim, so a best-effort pick is enough.
func parentRef(attr, self string, allTypes []string) (string, bool) {
	var base string
	switch {
	case strings.HasSuffix(attr, "_id"):
		base = strings.TrimSuffix(attr, "_id")
	case strings.HasSuffix(attr, "_arn"):
		base = strings.TrimSuffix(attr, "_arn")
	default:
		return "", false
	}
	if base == "" {
		return "", false
	}
	best := ""
	for _, t := range allTypes {
		if t == self {
			continue
		}
		if t == "aws_"+base {
			return t, true
		}
		if strings.HasSuffix(t, "_"+base) && (best == "" || len(t) < len(best)) {
			best = t
		}
	}
	return best, best != ""
}

// taggable mirrors internal/live/stamp's predicate of the same name (a
// top-level settable tags map of strings), which is also what
// TestTaggableSetAgainstRealSchemas pins against the real provider.
func taggable(block *configschema.Block) bool {
	if block == nil {
		return false
	}
	attr, ok := block.Attributes["tags"]
	if !ok || attr == nil {
		return false
	}
	if !attr.Optional && !attr.Required {
		return false
	}
	ty := attr.Type
	if !ty.IsMapType() {
		return false
	}
	et := ty.ElementType()
	return et == cty.String || et == cty.DynamicPseudoType
}

// identityAttrNames splits an identity schema's attributes into the
// required-for-import and optional-for-import sets, both sorted. In the
// plugin conversion Required/Optional carry exactly those wire flags
// (internal/plugin/convert/schema.go).
func identityAttrNames(obj *configschema.Object) (required, optional []string) {
	for name, attr := range obj.Attributes {
		switch {
		case attr.Required:
			required = append(required, name)
		case attr.Optional:
			optional = append(optional, name)
		}
	}
	sort.Strings(required)
	sort.Strings(optional)
	return required, optional
}

// marshal renders the survey deterministically: sorted rows, two-space
// indent, trailing newline, no HTML escaping.
func (s Survey) marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(s); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
