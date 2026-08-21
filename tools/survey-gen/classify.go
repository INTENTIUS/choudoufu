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

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/providers"
)

// The path vocabulary is SURVEY.md's, verbatim: its per-type table promises
// that nothing outside these tokens appears in the Path column, so the
// generated artifact speaks the same tokens.
//
// Five of the seven name a way identity is recovered. Two do not, and say
// so in the token itself: pathOps names the disposition of a type the rule
// excludes, and pathEnumerableUnbindable names a dead end.
//
// pathEnumerableUnbindable replaced "list + content match" on 2026-08-17,
// by ruling, because that string named a mechanism this fork does not have.
// The classifier reaches it from two facts and only two: the type takes no
// tags argument, and something can enumerate it - the provider's own native
// list resource, or the mapped CloudFormation type's Cloud Control list
// handler. Both are ENUMERATION facts. Neither is an admission fact, and
// the old token asserted one anyway.
//
// What actually happens to such a type at run time: internal/live/discovery
// binds a live object by reading the two ownership tags and by nothing else.
// Its Cloud Control leg lists the object, refines it with GetResource, and
// discards it when neither tag came back (cloudcontrol.go, ProblemNoTags, at
// severity error) - so it never reaches internal/live/foreign.Classify,
// whose content matching exists to surface a candidate for EXPLICIT
// adoption and, in that package's own words, never binds one automatically,
// "because inferring it from a content match would be exactly the guess the
// marker spec exists to forbid".
//
// So the honest reading of a row on this token is: it can be listed, and
// nothing can bind what the listing returns. That is admission debt with an
// address (issue #233 - the type needs somewhere to write a marker, or a
// record_store), not a fourth path.
//
// pathUniqueName joined the vocabulary on 2026-08-17, for the same reason
// pathEnumerableUnbindable was introduced: a type used to land there for a
// mechanism this fork now has. internal/live/discovery/uniquename.go binds a
// live object by comparing the configuration's declared name against the
// listed object's own name, for the narrow population where AWS documents
// that name as unique within the account and region - a claim
// internal/live/uniquename.Asserted reads off two independent texts, crossed
// by tools/row-gen/uniquename.go into internal/live/identity's table. It is
// not pathClientNamed: that token asserts the configuration states the
// import IDENTITY itself (identity.Derivable's strict, schema-provable
// rule), and every row this token reaches is ServerAssigned - the provider
// still mints the id or arn a later apply acts on. What the configuration
// states is a SEARCH KEY discovery can bind by instead of a tag, which is
// the same shape as pathMarker one level removed: marker binds by reading an
// ownership tag off a listing, pathUniqueName binds by reading a name off
// the same kind of listing. Both are ways in, so the token reads as one.
const (
	pathClientNamed          = "client-named"
	pathMarker               = "marker"
	pathParentDerived        = "parent-derived"
	pathEnumerableUnbindable = "enumerable, unbindable"
	pathAccountDerived       = "account-derived"
	pathUniqueName           = "unique-name"
	pathOps                  = "moves to Ops"
)

// opsExcluded is the one hand-written input to the classifier, per issue
// #25's own design: "what legitimately stays hand-written: the
// excluded-by-rule set ... credentials and waiters need the Ops judgment."
// No schema says a secret is unreadable after create, so the one entry left
// carries its reason here instead of a derivation.
//
// It is one rather than three after two rulings that went the same way, and
// the pattern in them is worth stating before the cases: an entry here is
// legitimate only when it records a fact about the resource's own CONTENTS
// that no schema carries. Neither "this is a waiter" nor "the marker would
// touch a secret" is that; both were judgments about how the resource is
// USED, and both were wrong.
//
// The maintainer's 2026-08-16 ruling removed aws_secretsmanager_secret_version,
// which read "credential: secret_id plus a server-assigned version UUID, the
// secret unreadable after create". The ownership marker goes into a TAG,
// never into the secret, so nothing about marking a secret version reads or
// exposes its contents and the credential-material rationale never applied
// to it. It was also never on CLAUDE.md's sanctioned list, which is exactly
// four types (aws_iam_access_key, aws_iot_certificate,
// aws_ivs_playback_key_pair, aws_appstream_directory_config) and does not
// grow. So the type is admission debt like any other and classifies from
// its own schema below, which on the pinned release lands it on
// "enumerable, unbindable": untaggable, but the provider does ship a native
// list resource for it, and its identity schema requires the server-minted
// version_id beside the configuration's own secret_id. That is the same
// shape as the rest of tools/row-gen/rejected.json's server-minted
// composites, so it stays unadmitted for their reason - no marker-capable
// argument, blocked on #233 - reached by derivation rather than by veto.
//
// The maintainer's 2026-08-17 ruling removed aws_acm_certificate_validation,
// which read "waiter: records only that DNS validation finished; waiting
// belongs to the lifecycle layer". The resource gates whether the
// certificate is usable, so an estate does care about it, and "waiter" was a
// statement about what the resource MEANS rather than about what can name
// it. Classified from its own schema it lands on parent-derived: the
// provider's identity schema for it requires exactly certificate_arn, which
// is a required argument of the type and points at aws_acm_certificate - a
// taggable, admitted type. That is the same shape as
// aws_s3_bucket_policy's, where the parent genuinely IS the identity, and
// unlike the parent-derived population refuted on 2026-08-17 (see
// HANDOFF.md) this one is already in reach: identity.Derivable already
// admits the type from the schemas, and the veto in
// [identity.MarkerlessTypes] never covered it, so the row was Ops-labelled
// while the resolver resolved it anyway. Removing the entry makes the
// artifact agree with the code rather than changing what the code does.
//
// aws_iam_access_key stays because its distinguishing fact is not the
// marker at all: the secret half is returned once at create and is
// unreadable afterwards, so a live read cannot reconstruct what the
// configuration is entitled to hold. That is a statement about the
// resource's own contents, which no schema carries, and it is on the
// sanctioned list.
var opsExcluded = map[string]string{
	"aws_iam_access_key": "credential: the secret half is unreadable after create, and an external holder makes set semantics inapplicable",
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
//
// service answers "which AWS service does this Terraform type belong to",
// for parentRef's suffix-match affinity (issue #167). It is passed in rather
// than loaded here for the same reason internal/live/identity takes one: the
// fact lives in live/mapping.json, and a classifier that reaches for an
// artifact on its own is harder to test than one handed the answer.
// enumerate is the same arrangement for the Cloud Control listing question;
// see cfnEnumeration.
func buildSurvey(schema providers.GetProviderSchemaResponse, roster []string, service identity.ServiceOf, enumerate cfnEnumeration, importable map[string]bool) Survey {
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

	// The CFN service per type, for parentRef's suffix-match affinity
	// (issue #167). The embedded roster carries live/mapping.json, which is
	// the only thing that knows two differently-prefixed Terraform types
	// belong to one AWS service.
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
		row := classify(typeName, schema, derivable, allTypes, service, enumerate, importable[typeName])
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
// strongest path first (client-named, marker, parent-derived, account-
// derived, unique-name; a type failing every rule leaves the resource
// model).
//
// The mechanical rules, in the order applied:
//
//  1. The hand-curated Ops exclusion (credentials and waiters) wins
//     outright; no schema carries that judgment.
//  2. The fork's identity table builds the identity from configuration plus
//     the run's account or region, which is account-derived. Also not a
//     schema judgment - see cloudValuesOf.
//  3. The fork's identity table names a unique-name binding for the type,
//     which is unique-name. Also not a schema judgment - see uniqueNameOf.
//  4. identity.Derivable proves the identity fully client-assigned. Within
//     that set, a required identity attribute that names another managed
//     type's identity (an *_id or *_arn whose base names a resource type)
//     makes the path parent-derived; otherwise client-named.
//  5. An identity schema the strict rule cannot prove client-assigned falls
//     through to what discovery can do: marker when the type is taggable,
//     which is the only one of the three that admits anything; enumerable,
//     unbindable when it is untaggable but something can list it; moves to
//     Ops when it is untaggable and nothing can list it either.
//  6. No identity schema at all: the same discovery fallback, with the
//     evidence saying the identity side is unreadable from schemas.
//
// enumerate answers "can this type be listed, and how", over the same two
// registry facts internal/live/discovery's own enumeration-source selection
// reads. It is passed in for the same reason service is: the fact lives in
// live/mapping.json and live/registry.json, and a classifier handed the
// answer is easier to test than one that reaches for an artifact.
func classify(typeName string, schema providers.GetProviderSchemaResponse, derivable map[string]identity.DerivableType, allTypes []string, service identity.ServiceOf, enumerate cfnEnumeration, importable bool) Row {
	rs := schema.ResourceTypes[typeName]
	_, hasList := schema.ListResourceTypes[typeName]

	row := Row{
		Type: typeName,
		Signals: Signals{
			Taggable:       taggable(rs.Block),
			ListResource:   hasList,
			IdentitySchema: rs.IdentitySchema != nil,
			Importable:     importable,
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

	// The unique-name path, and the second place this classifier reads the
	// fork's identity table rather than the provider's schemas. It has to:
	// the fact this branch needs - that AWS itself refuses to issue this
	// type's configured name twice within an account and region - is not in
	// any provider schema. It comes from crossing two independent AWS texts
	// (the provider's own argument reference and the CloudFormation
	// registry's property description), which is tools/row-gen/uniquename.go's
	// job and internal/live/uniquename.Asserted's predicate, not this tool's.
	// That crossing is asserted once, in internal/live/identity's table, as
	// [identity.TypeIdentity.UniqueName] - so this reads the assertion
	// instead of restating the crossing, the same way the account-derived
	// branch above reads a cloud-value assertion rather than re-deriving it.
	if arg, prop, ok := uniqueNameOf(typeName); ok {
		row.Path = pathUniqueName
		row.Evidence = fmt.Sprintf(
			"the provider's argument reference for %s and the CloudFormation registry's %s schema independently document it as unique within the account and region, so discovery binds a live object by that name rather than by an ownership tag",
			arg, prop)
		return row
	}

	if d, ok := derivable[typeName]; ok {
		var parents []string
		for _, attr := range d.IdentityAttrs {
			if parent, ok := parentRef(attr, typeName, allTypes, service); ok {
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
	switch rs.IdentitySchema {
	case nil:
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

	switch cfnType, scoping, listable := enumerate(typeName); {
	case row.Signals.Taggable:
		row.Path = pathMarker
		row.Evidence = identityNote + "; taggable, so recoverable by tag-filtered list"
	case hasList:
		row.Path = pathEnumerableUnbindable
		row.Evidence = identityNote + "; untaggable, so the native list resource can enumerate it but no discovery leg can bind what it returns - binding reads the two ownership tags and this type has nowhere to write them"
	case listable && len(scoping) == 0:
		row.Path = pathEnumerableUnbindable
		row.Evidence = identityNote + "; untaggable, so Cloud Control listing " + cfnType + " with no scoping input enumerates it but no discovery leg can bind what it returns - binding reads the two ownership tags and this type has nowhere to write them"
	case listable:
		row.Path = pathOps
		row.Evidence = identityNote + "; untaggable, no native list resource, and Cloud Control's list handler for " + cfnType +
			" requires " + strings.Join(scoping, " and ") + " as scoping input, which no enumeration leg supplies today"
	default:
		row.Path = pathOps
		row.Evidence = identityNote + "; untaggable, no native list resource and no Cloud Control list handler, so no admission path recovers it"
	}
	return row
}

// cfnEnumeration reports how Cloud Control can enumerate a Terraform type:
// the CFN type ListResources would be asked for, the scoping input that
// type's list handler requires (empty when it needs none), and whether the
// registry names a list handler for it at all.
//
// It exists because live/survey-full.json spent 699 rows asserting "no
// admission path recovers it" from one signal - the PROVIDER's own native
// list resource (GetProviderSchemaResponse.ListResourceTypes) - while
// internal/live/discovery/discovery.go's scanType has read two signals
// since issue #47: the native list resource first, then, for a type that
// has none, the mapped CFN type's own Cloud Control list handler
// (cloudControlSource -> registry.Roster.EnumerationSource). A survey whose
// enumeration question is narrower than the code's answers it wrong, and
// says so in the artifact every downstream document quotes.
//
// The signature deliberately merges registry.Roster's two accessors rather
// than exposing them separately: EnumerationSource is true only for a list
// handler needing no input and EnumerationSourceScoped only for one that
// does, so a caller reading either alone sees "not listable" for half the
// listable set - which is the shape of mistake this whole function exists
// to correct.
type cfnEnumeration func(tfType string) (cfnType string, requiredInput []string, listable bool)

// noEnumeration is the cfnEnumeration a caller with no registry roster
// passes: nothing is listable through Cloud Control, which reproduces the
// classifier's pre-#47 behaviour exactly. It is the honest answer for a
// test fixture whose types are not in live/mapping.json at all, not a
// silent default - buildSurvey takes the function rather than a nilable
// roster so that "no roster" is a decision at the call site.
func noEnumeration(string) (string, []string, bool) { return "", nil, false }

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

// uniqueNameOf returns the configuration argument and the CloudFormation
// property path a type's identity-table entry names for unique-name
// discovery binding, and whether the table carries one at all. See the
// unique-name branch in classify. The table's UniqueName field is the same
// crossing tools/row-gen/uniquename.go computed to admit the row in the
// first place (live/registry.json's unique_name_property against
// live/import-grammar.json's declared_unique) - this reads that result
// rather than recomputing the crossing.
func uniqueNameOf(typeName string) (arg, property string, ok bool) {
	entry, ok := identity.LookupType(typeName)
	if !ok || !entry.UniqueName.Set() {
		return "", "", false
	}
	return entry.UniqueName.Attrs[0], entry.UniqueName.Property, true
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
// Two shapes of match, and they are held to different standards, because
// only one of them is a whole-name fact (issue #167).
//
// An exact "aws_<base>" match is the type itself under another name -
// vpc_id naming aws_vpc - and is allowed to cross AWS services, which is
// what keeps aws_neptunegraph_private_graph_endpoint pointed at aws_vpc.
//
// A suffix match is a guess, and must share the child's CloudFormation
// service. Without that requirement this rule searched every type for the
// shortest name ending in the base, and "resource_arn" resolved to
// aws_api_gateway_resource five separate times, "cluster_arn" to
// aws_dax_cluster for MSK, "collection_id" to aws_route53_cidr_collection
// for Rekognition. The service is the right affinity test rather than the
// Terraform prefix for the same reason it is in
// internal/live/identity/parent.go: AWS::EC2::VolumeAttachment and
// AWS::EC2::Volume are one service and two Terraform prefixes.
//
// The comment this replaced said "the named parent is evidence prose, not a
// wiring claim, so a best-effort pick is enough". The NAME is prose. The ok
// is not: classify above reads it to choose between pathParentDerived and
// pathClientNamed, and both are rendered into live/SURVEY.md's summary
// table.
//
// A candidate no branch accepts yields no parent, and there is deliberately
// no exception table. aws_networkmanager_prefix_list_association ->
// aws_ec2_managed_prefix_list is a correct cross-service SUFFIX match and is
// lost that way; that is the honest cost, per #129's own rule.
//
// The exact branch keeps one known-wrong match, and it is left in
// deliberately: aws_ssoadmin_account_assignment's instance_arn resolves to
// aws_instance, because "instance" is as generic a noun as "resource" was
// and an exact match cannot tell an IAM Identity Center instance from an EC2
// one. Refusing it needs either a stop-list of generic nouns or affinity on
// the exact branch, and affinity there would also lose
// aws_neptunegraph_private_graph_endpoint -> aws_vpc, which is correct.
//
// It costs nothing that matters. The row's Path is parent-derived either
// way - aws_ssoadmin_account_assignment genuinely is composed of parent
// identities - so only the Evidence prose names the wrong type, which is
// the one case where the comment this replaced was right about a
// best-effort pick being enough. Measured after this change: 47
// parent-derived rows, 2 cross-service links, both from the exact branch,
// one correct and this one.
func parentRef(attr, self string, allTypes []string, service identity.ServiceOf) (string, bool) {
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
	if exact := "aws_" + base; exact != self {
		for _, t := range allTypes {
			if t == exact {
				return t, true
			}
		}
	}

	best := ""
	for _, t := range allTypes {
		if t == self {
			continue
		}
		if !identity.SameServiceAffinity(self, t, service) {
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
