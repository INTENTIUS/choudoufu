// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/servicetags"
	"github.com/intentius/choudoufu/internal/providers"
)

// What this artifact records, and what it no longer does (#696).
//
// Every row is the provider's own facts about one resource type, read off
// one GetProviderSchema response plus one ImportResourceState probe: the raw
// signals (a settable tags argument, a native list resource, a resource
// identity schema, a classic Importer), the identity schema's attribute
// composition, and identity.Report's verdict on whether those schemas alone
// settle that the configuration names the resource. Those are outside
// evidence - no row-gen output writes them - which is why the generators
// and guards downstream of this one read them, and why they stay.
//
// Until #696 each row also carried a "path": a seven-token classification
// of how a live object's identity is recovered (client-named, marker,
// parent-derived, account-derived, unique-name, enumerable-unbindable, moves
// to Ops), with a sentence of evidence per row, mirrored by hand in
// live/SURVEY.md's per-type table. That was a second vocabulary for the axis
// the readiness tiers A-D already classify (tools/readiness-gen,
// live/readiness.json), and the two had to be reconciled in prose wherever
// they met. The tiers are the one that stays. A reader that needs one of
// the path's inputs reads the input itself:
//
//   - "is it taggable": signals.taggable;
//   - "do the schemas prove the configuration names it": admission ==
//     "schema" (identity.Derivable's verdict, which is what the path's
//     client-named and parent-derived tokens both meant);
//   - "does the identity table build it from configuration plus the run's
//     account or region, or bind it by a unique name": the table itself,
//     identity.LookupType, which is where the path read those two facts
//     from in the first place;
//   - "which tier is it in": live/readiness.json.

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

	// Accepted is the ISO date a human ran the generator with -accept and
	// ratified the rows below - the same vocabulary
	// tools/registry-gen/pin.go's SpecPin.Accepted uses, "so a diff reads
	// as a decision" rather than one regeneration silently replacing
	// another (issue #37, increment 1). Empty unless -accept was passed on
	// the run that produced this file: neither this tool nor its tests
	// read the previously committed artifact as an input, so regenerating
	// without -accept drops any previously accepted date out of the diff
	// instead of carrying it forward unreviewed.
	Accepted string `json:"accepted,omitempty"`

	// Counts are the roster-wide raw-signal totals, the figures SURVEY.md's
	// "Raw signals" section records by hand - and, when Accepted is set,
	// the reviewed counts a human ratified alongside it.
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
	// Type is the resource type name.
	Type string `json:"type"`

	// Signals are the per-type raw signals.
	Signals Signals `json:"signals"`

	// Identity is the identity attribute composition from the provider's
	// resource identity schema, absent when the provider ships none.
	Identity *IdentityAttrs `json:"identity,omitempty"`

	// Admission is what would let the fork's identity table carry this type
	// with no hand-written row: "schema" when the provider's schemas settle
	// that the configuration names the resource, "needs-config-signal" when
	// they leave that to whether a configuration sets the identity
	// attributes above (the Optional+Computed cohort, which is most of the
	// name-prefix idiom's exceptions). Absent when neither admits it. See
	// internal/live/identity's Report.
	Admission string `json:"admission,omitempty"`
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

	// Importable: the provider's classic ImportState reports a real
	// Importer for the type rather than "doesn't support import" (SDKv2) or
	// "Resource Import Not Implemented" (the plugin framework) - schemas.go's
	// probeImportability, one extra ImportResourceState RPC per type with a
	// syntactically invalid dummy ID, over the same provider connection this
	// tool already launches to read the schema.
	//
	// Issue #331: a resource identity schema is not proof of this. Six types
	// in the identity_schema_wire_only bucket carry one and no documented
	// Import section; the audit that opened this field found two of the six
	// have no Importer at all (aws_iam_policy_attachment,
	// aws_acm_certificate_validation) and would hard-fail
	// "resource ... doesn't support import" the moment a real migrate calls
	// ImportResourceState, while the other four import fine. Taggability is
	// not proof either: aws_iot_ca_certificate and aws_lightsail_domain are
	// both admitted on other evidence (a ratified row, an enumeration path)
	// and both have no Importer. This is the one signal that answers the
	// question directly instead of inferring it from something else.
	Importable bool `json:"importable"`

	// ServiceList: the service's own list API enumerates the type and the
	// service's own tag API reads its marker - internal/live/servicetags'
	// route tables, which internal/live/discovery's service-list leg drives
	// (GitHub issues #1477 and #1496). Both halves are required: a list
	// route with no tag-read route enumerates what nothing can bind. Omitted
	// when false.
	ServiceList bool `json:"service_list,omitempty"`
}

// IdentityAttrs is the identity schema's attribute composition.
type IdentityAttrs struct {
	// RequiredForImport are the attributes the provider needs to import
	// the resource; OptionalForImport the ones it can fill in itself
	// (account_id and region, in the AWS provider). Both sorted.
	RequiredForImport []string `json:"required_for_import"`
	OptionalForImport []string `json:"optional_for_import,omitempty"`

	// NotResourceAttributes are the identity schema's attributes, required
	// or optional, that the resource schema has no top-level attribute or
	// block by the name of. Sorted; omitted when every identity attribute
	// is also a resource attribute, which is the case for 181 of the 479
	// identity schemas hashicorp/aws 6.59.0 ships.
	//
	// The two schemas are different vocabularies that mostly coincide. The
	// identity schema names the attributes of the identity OBJECT the
	// provider returns from a list call and accepts on an import by
	// identity; the resource schema names what a configuration can
	// reference as aws_type.name.attr. account_id is the common case of a
	// name in the first and not the second (296 types), and three types
	// carry a required identity attribute the resource spells differently:
	// aws_osis_pipeline's identity is {name} and its resource attribute is
	// pipeline_name; aws_securityhub_member's is {member_account_id}
	// against account_id; aws_organizations_delegated_administrator's is
	// {delegated_account_id} against account_id.
	//
	// It is recorded here, where the resource schema is in hand, because
	// tools/row-gen has to keep these names OUT of
	// [identity.TypeIdentity.IdentityAttrs] - that field is defined as
	// resource attributes another resource may reference, and a name from
	// the identity vocabulary there is a reference that resolves against
	// nothing (identity.VerifyTable's FindingAttributeNotInSchema, which
	// fired on aws_osis_pipeline.name at the table's own pin).
	NotResourceAttributes []string `json:"not_resource_attributes,omitempty"`
}

// buildSurvey derives one row per roster type from the provider's schemas.
func buildSurvey(schema providers.GetProviderSchemaResponse, roster []string, importable map[string]bool) Survey {
	// The derivability report over the same schemas, with no configuration
	// to read: this tool surveys a provider release, not an estate, so the
	// cohort the schemas cannot settle comes back "needs-config-signal"
	// naming the arguments a configuration would have to set. The strict
	// "schema" verdict is identity.Derivable's - the one classifier that
	// already knows the Optional+Computed trap (aws_s3_bucket.bucket and
	// aws_vpc.id are the same shape in a legacy-SDK schema and opposite
	// answers; see internal/live/identity/doc.go).
	report := identity.Report(schema.ResourceTypes, nil)

	s := Survey{
		Provider:        providerSource,
		ProviderVersion: providerVersion,
		GeneratedBy:     "tools/survey-gen (go run ./tools/survey-gen)",
	}

	sorted := append([]string(nil), roster...)
	sort.Strings(sorted)
	for _, typeName := range sorted {
		row := classify(typeName, schema, importable[typeName])
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

// classify derives one type's row: the raw signals and the identity
// composition, each read straight off the provider's schemas.
func classify(typeName string, schema providers.GetProviderSchemaResponse, importable bool) Row {
	rs := schema.ResourceTypes[typeName]
	_, hasList := schema.ListResourceTypes[typeName]

	row := Row{
		Type: typeName,
		Signals: Signals{
			Taggable:       taggable(rs.Block),
			ListResource:   hasList,
			IdentitySchema: rs.IdentitySchema != nil,
			Importable:     importable,
			ServiceList:    serviceRouted(typeName),
		},
	}
	if rs.IdentitySchema != nil {
		required, optional := identityAttrNames(rs.IdentitySchema)
		row.Identity = &IdentityAttrs{
			RequiredForImport:     required,
			OptionalForImport:     optional,
			NotResourceAttributes: notResourceAttributes(rs.Block, required, optional),
		}
	}
	return row
}

// taggable is [markers.Taggable] - the predicate the run itself applies -
// and no longer a copy of the four clauses it had when this was written.
//
// The copy was missing the fifth, which #243 added: a tags map whose keys
// the provider documents as naming objects that must already exist is
// schema-identical to a free-form one and is not a marker surface. Measured
// against the real schemas, the difference is nothing on hashicorp/aws
// 6.59.0 - none of its 847 tags attributes carries a description at all -
// and 17 resource types on hashicorp/google 7.44.0, where every one of the
// 26 tags attributes is a Resource Manager tag binding. The signal this
// function writes into live/survey-full.json is what row-gen's markerless
// rule reads, so on any survey of that provider the copy would have
// recorded 17 types as marker-carrying that the run refuses to stamp.
func taggable(block *configschema.Block) bool {
	return markers.Taggable(block)
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

// notResourceAttributes is [IdentityAttrs.NotResourceAttributes]: the
// identity attributes, required and optional together, that block has no
// top-level attribute or nested block for. A nil block (a type the provider
// serves an identity schema for but no resource schema) makes every identity
// attribute a non-resource one, which is the honest answer rather than a
// special case. The result is sorted and nil when empty, so the field is
// omitted from the artifact for the types where the two vocabularies agree.
func notResourceAttributes(block *configschema.Block, required, optional []string) []string {
	var out []string
	for _, name := range append(append([]string{}, required...), optional...) {
		if block != nil {
			if _, ok := block.Attributes[name]; ok {
				continue
			}
			if _, ok := block.BlockTypes[name]; ok {
				continue
			}
		}
		out = append(out, name)
	}
	sort.Strings(out)
	return out
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

// serviceRouted reports whether internal/live/discovery's service-list leg
// recovers typeName: the service's listing reaches the object and the
// service's own tag API reads the marker off it, because IAM's list
// operations drop tags by design (internal/live/servicetags' doc comment).
//
// It reads servicetags' route tables through the same accessors discovery's
// leg reads them through, so the survey and the run cannot disagree about
// which types the leg covers. The IAM client is never called: ListRoute and
// Route consult only the tables.
func serviceRouted(typeName string) bool {
	r := servicetags.NewIAM(nil)
	return r.ListRoute(typeName) && r.Route(typeName)
}
