// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// importGrammarRow is the slice of live/import-grammar.json's per-type shape
// (tools/importdocs-gen, issue #52/#55) this tool reads: whether the
// provider's own Import documentation shows the ID built from configuration
// arguments, the literal example that showed it, and the argument names the
// ID's segments matched against Argument Reference (Arguments) - the source
// resolveArgName consults ahead of the carve seed (issue #52's second half:
// retiring tools/mapping-gen/carve-seed.json in the import-grammar rows it
// makes redundant).
type importGrammarRow struct {
	TFType              string   `json:"tf_type"`
	ImportIDExample     string   `json:"import_id_example"`
	ComposedOfArguments *bool    `json:"composed_of_arguments"`
	Separator           *string  `json:"separator"`
	Arguments           []string `json:"arguments"`

	// OmittedFallbacks is the Import section's own omitted-argument
	// fallback statement, scraped by tools/importdocs-gen ("if you omit
	// `event_bus_name`, the `default` event bus will be used" ->
	// {"event_bus_name": "default"}), recorded only for arguments the
	// composite's own Arguments list names.
	OmittedFallbacks map[string]string `json:"omitted_fallbacks"`

	// ArgumentReference, ArgumentsInOrder, IdentitySchemaRequired and
	// IdentitySchemaOptional are the widened scrape's own evidence (issue:
	// the decisive identity evidence for a scrape-gap type sits in the
	// Argument Reference or the Identity Schema, not the Import section's
	// prose alone) - see tools/importdocs-gen/artifact.go's Row for what
	// each one means; the shapes here are the same, just re-declared for
	// this package's own JSON decode the way the five fields above already
	// are.
	ArgumentReference      []argumentRefEntry `json:"argument_reference"`
	ArgumentsInOrder       []string           `json:"arguments_in_doc_order"`
	IdentitySchemaRequired []string           `json:"identity_schema_required"`
	IdentitySchemaOptional []string           `json:"identity_schema_optional"`

	// IDTemplate mirrors tools/importdocs-gen/idtemplate.go's IDTemplate
	// (issue #172): the documented ARN/URL import ID decomposed into
	// literals, Cloud region/account slots, and per-segment-attributed tail
	// arguments. Nil for the ordinary short-ID example.
	IDTemplate *idTemplate `json:"import_id_template"`

	// IDParts is the documented import ID's per-segment source attribution
	// (issue #132): the segment names the Import section's own prose gives,
	// each attributed to the doc section that defines it ("argument",
	// "attribute" or "unknown"), present only when the named segments
	// account for every segment of the documented example - see
	// tools/importdocs-gen/parse.go's idParts.
	IDParts []idPart `json:"id_parts"`

	// SoleIDPart and ArgumentNamesAnyDepth are the second evidence source
	// the markerless veto reads (issue #249, markerless.go) - see
	// tools/importdocs-gen/soleid.go for what each one means. SoleIDPart
	// is IDParts at the one arity IDParts' own gate refuses;
	// ArgumentNamesAnyDepth is the refutation set that keeps a nested
	// block's argument from being read as a server-minted segment.
	SoleIDPart            *idPart  `json:"sole_id_part"`
	ArgumentNamesAnyDepth []string `json:"argument_names_any_depth"`

	// SoleIDCloudValue mirrors tools/importdocs-gen/cloudsingleton.go's Row
	// field of the same name: the cloud property the documented import ID
	// IS, in its entirety, spelled with identity.CloudValue's own string.
	// Empty for all but 25 of the 1699 rows. tryCloudSingletonID is its one
	// reader; see that function and the scrape's own doc comment for why
	// the vocabulary is the region alone.
	SoleIDCloudValue string `json:"sole_id_cloud_value"`

	// EvidenceExcerpt is the Import section's own text, pinned verbatim at
	// scrape time (tools/importdocs-gen/artifact.go's Evidence). Issue #176's
	// R3 corroboration reads its "using ..." sentences the same way
	// tools/importdocs-gen/prosename.go does, to catch a doc whose own prose
	// names the documented ID as the resource's own server-minted identifier.
	EvidenceExcerpt string `json:"evidence_excerpt"`

	// ExampleArguments are the Example Usage section's own top-level literal
	// values (tools/importdocs-gen/example.go), pinned so a consumer can test
	// a documented import ID against the value the doc itself configures an
	// argument with - issue #176's R3 corroboration.
	ExampleArguments []exampleArgument `json:"example_arguments"`
}

// exampleArgument mirrors tools/importdocs-gen/example.go's ExampleArgument:
// one Example Usage argument's path and literal value. Only top-level string
// values (len(Path)==1, IsString) are ever consulted here.
type exampleArgument struct {
	Path     []string `json:"path"`
	Value    string   `json:"value"`
	IsString bool     `json:"is_string"`
}

// idTemplate and idTemplateSegment mirror tools/importdocs-gen/
// idtemplate.go's IDTemplate/TemplateSegment: Kind is "arn" or "url";
// exactly one of a segment's Literal/Cloud/Argument/Unattributed is set,
// with AttributedBy naming the doc signal that linked an Argument segment -
// the confidence-tier vocabulary tryAssembledTemplate keys on (see the
// attrBy* constants below).
type idTemplate struct {
	Kind     string              `json:"kind"`
	Segments []idTemplateSegment `json:"segments"`
}

type idTemplateSegment struct {
	Literal      string `json:"literal"`
	Cloud        string `json:"cloud"`
	Argument     string `json:"argument"`
	AttributedBy string `json:"attributed_by"`
	Unattributed string `json:"unattributed"`
}

// The AttributedBy values tools/importdocs-gen/idtemplate.go writes. The
// first two are statements the segment's own text makes (the placeholder
// spells or abbreviates the argument's name); the other two, not named
// here because only their absence matters, are contextual
// ("self-placeholder-required-name", "example-usage-value").
const (
	attrByPlaceholderName   = "placeholder-names-argument"
	attrByPlaceholderAbbrev = "placeholder-abbreviates-argument"
)

// idPart mirrors tools/importdocs-gen/parse.go's IDPart: one prose-named
// segment of the documented import ID and which doc section defines it.
type idPart struct {
	Token  string `json:"token"`
	Source string `json:"source"`
}

// idPartSourceAttribute names a segment the doc's own Attribute Reference
// exports; idPartSourceOwnID names a segment that is the resource's own
// identifier, stated in plain prose on the resource's own page ("IPSet ID"
// on aws_guardduty_ipset's) - both are server-provided values no
// configuration argument supplies. The other two values ("argument",
// "unknown") are only ever tested for indirectly, so they carry no
// constant here.
const (
	idPartSourceAttribute = "attribute"
	idPartSourceOwnID     = "own-id"
)

// docNamesServerSegment reports whether the grammar row's per-segment
// attribution names at least one segment as the doc's own Attribute
// Reference export. When it does, the doc itself is saying the import ID
// carries a server-provided value, so the ID is not reconstructible from
// configuration - the fact that separates aws_connect_queue
// ("instance_id:queue_id", queue_id exported) from the structurally
// identical-looking aws_lambda_alias ("function_name/alias", alias being
// prose shorthand for the `name` argument, attributed "unknown"). A
// non-empty IDParts already guarantees the named segments cover the whole
// documented example (the scrape's own arity gate), so no further arity
// check is needed here.
func docNamesServerSegment(g importGrammarRow) bool {
	for _, part := range g.IDParts {
		if part.Source == idPartSourceAttribute || part.Source == idPartSourceOwnID {
			return true
		}
	}
	return false
}

// docMintedSegment reports the segment of the documented import ID that
// the provider's own documentation says the SERVER supplies, over both
// arities: IDParts for a composite, SoleIDPart for a one-segment ID.
//
// It is docNamesServerSegment's stricter sibling, and the differences are
// the point:
//
//   - It reads SoleIDPart as well, so a type whose whole documented ID is
//     one opaque value is answerable at all. docNamesServerSegment cannot
//     see those - IDParts is empty for every one of them - which is why
//     the veto this feeds used to stop at composites, and why it missed
//     the three types issue #249's own recommendation named.
//   - It subtracts ArgumentNamesAnyDepth. A segment the doc attributes to
//     the Attribute Reference, whose name is ALSO an Argument Reference
//     bullet inside some nested block, is configuration-supplied; the
//     attribution only landed on "attribute" because idParts' argument set
//     is the top-level one. Calling that segment server-minted is the
//     exact misread issue #242 corrected, and it is a misread in the
//     dangerous direction here, because this evidence vetoes a type.
//   - It defers to the provider's own identity schema, which outranks
//     any docs paragraph - see identitySchemaIsAllArguments.
//
// Both callers of docNamesServerSegment are left alone: they answer a
// different question (does this composite's registry-sourced
// server-assigned classification survive the doc's own attribution), and
// widening their evidence would change proposals rather than vetoes.
func docMintedSegment(g importGrammarRow) (string, bool) {
	configArgs := make(map[string]bool, len(g.ArgumentNamesAnyDepth))
	for _, n := range g.ArgumentNamesAnyDepth {
		configArgs[normalizeName(n)] = true
	}
	if identitySchemaIsAllArguments(g, configArgs) {
		return "", false
	}
	parts := g.IDParts
	if g.SoleIDPart != nil {
		parts = append(append([]idPart(nil), parts...), *g.SoleIDPart)
	}
	for _, part := range parts {
		if part.Source != idPartSourceAttribute && part.Source != idPartSourceOwnID {
			continue
		}
		if namesAConfigurationArgument(part.Token, configArgs) {
			continue
		}
		return part.Token, true
	}
	return "", false
}

// identitySchemaIsAllArguments reports whether the provider's own
// Identity Schema names a required identity whose every component is a
// configuration argument. When it does, the ID is reconstructible from
// configuration whatever a second documented import form says, and no
// prose segment can establish otherwise.
//
// This is the provider's structured answer outranking a docs paragraph -
// the same order classify.go's resolveArgName already states - and it is
// load-bearing for a page that documents two import forms.
// aws_neptunegraph_private_graph_endpoint documents both "using the
// `graph_identifier` and `vpc_id`" (both arguments) and "using the
// `private_graph_endpoint_identifier`" (minted); the first is what the
// v1.12 identity block declares, so the identity is computable and the
// second form is a convenience, not a wall.
func identitySchemaIsAllArguments(g importGrammarRow, configArgs map[string]bool) bool {
	if len(g.IdentitySchemaRequired) == 0 {
		return false
	}
	for _, name := range g.IdentitySchemaRequired {
		if !configArgs[normalizeName(name)] {
			return false
		}
	}
	return true
}

// namesAConfigurationArgument reports whether a prose segment name lands
// on a configuration argument, over the same word-suffix scan the scrape
// itself attributes a plain-prose segment with (tools/importdocs-gen's
// attributePlainPart): the whole token first, then each shorter suffix of
// its words.
//
// The suffix scan is what makes this a refutation rather than a spot
// check. aws_fms_admin_account documents its import "using the account
// ID"; the whole token is "the account ID", which matches no argument
// name, while its two-word suffix "account ID" is exactly the
// `account_id` argument the page declares. Comparing only the whole token
// would have vetoed a type whose identity the configuration supplies.
func namesAConfigurationArgument(token string, configArgs map[string]bool) bool {
	words := strings.Fields(token)
	for k := len(words); k >= 1; k-- {
		if configArgs[normalizeName(strings.Join(words[len(words)-k:], ""))] {
			return true
		}
	}
	return false
}

// normalizeName is tools/importdocs-gen's own normalize: the comparison
// form both sides of a name match reduce to, so "REST-API-ID" and
// "rest_api_id" are one name. Re-declared here for the same reason
// importGrammarRow's fields are - this package decodes the artifact
// itself rather than importing the generator that wrote it.
func normalizeName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// serverSegmentTokens names the attribute-sourced segments, for the note a
// docNamesServerSegment-based decision leaves behind.
func serverSegmentTokens(g importGrammarRow) string {
	var out []string
	for _, part := range g.IDParts {
		if part.Source == idPartSourceAttribute {
			out = append(out, part.Token)
		}
	}
	return strings.Join(out, ", ")
}

// argumentRefEntry mirrors tools/importdocs-gen/artifact.go's
// ArgumentRefEntry: one Argument Reference bullet's name and its
// Required/ForceNew/ServerAssignedIfAbsent marking.
type argumentRefEntry struct {
	Name     string `json:"name"`
	Required bool   `json:"required"`
	ForceNew bool   `json:"force_new"`

	// ServerAssignedIfAbsent mirrors tools/importdocs-gen/parse.go's
	// ArgumentRefEntry field of the same name: the bullet's own prose
	// states the provider assigns this Optional argument a fresh value
	// (not a literal default - see Component.Default's own scrape,
	// OmittedFallbacks above) when the configuration omits it. See #190
	// and internal/live/identity/table.go's Component.ServerAssignedIfAbsent,
	// the field this evidence feeds at emit time (emit.go's
	// mergeServerAssigned).
	ServerAssignedIfAbsent bool `json:"server_assigned_if_absent"`

	// CloudDefault mirrors tools/importdocs-gen/parse.go's ArgumentRefEntry
	// field of the same name: the bullet's own prose states that omitting
	// this Optional argument selects a property of the cloud the run is
	// pointed at, spelled with identity.CloudAccountID's and
	// identity.CloudRegion's own string values. See emit.go's
	// mergeCloudDefault, the merge this evidence feeds.
	CloudDefault string `json:"cloud_default"`
}

type importGrammarArtifact struct {
	Rows []importGrammarRow `json:"rows"`
}

// loadImportGrammar reads live/import-grammar.json and indexes it by TF
// type name. A missing file is not an error: the demotion hook this feeds
// (classifyAll's applyImportGrammarDemotions) is additive evidence, not a
// required input, and every artifact this tool already reads predates
// import-grammar.json's own introduction - see #55's sequencing note that
// this source lands ahead of the orchestrator that would otherwise
// guarantee its presence.
func loadImportGrammar(path string) (map[string]importGrammarRow, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]importGrammarRow{}, nil
		}
		return nil, err
	}
	var art importGrammarArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := make(map[string]importGrammarRow, len(art.Rows))
	for _, r := range art.Rows {
		out[r.TFType] = r
	}
	return out, nil
}
