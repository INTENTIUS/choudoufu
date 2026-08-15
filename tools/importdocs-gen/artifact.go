// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"sort"
)

// Row is one TF resource type's parsed import grammar (issue #52, the
// import-docs source; issue #55's "114 hand separators" and "rule-1
// verification tax" replacement).
type Row struct {
	// TFType is the resource type name, e.g. "aws_iam_role_policy_attachment".
	TFType string `json:"tf_type"`

	// ImportIDExample is the literal ID string the doc's own `terraform
	// import` example (or import-block `id = `) shows.
	ImportIDExample string `json:"import_id_example"`

	// Separator is the single character that joins a composite ID's
	// segments, when the doc states or shows one unambiguously. Null when
	// the ID is not composite, or when it is but no source here resolves
	// the character with confidence - never a guess.
	Separator *string `json:"separator"`

	// ComposedOfArguments is true when the ID is built from configuration
	// arguments the doc's own Argument Reference names, false when it is
	// an opaque server-assigned value (matches nothing in Argument
	// Reference), and null when neither is resolvable from this doc.
	ComposedOfArguments *bool `json:"composed_of_arguments"`

	// Arguments names the configuration arguments the ID's segments
	// matched against, when ComposedOfArguments is true. May be shorter
	// than the ID's true segment count - see classifyGrammar's doc
	// comment - and is always empty when ComposedOfArguments is not true.
	Arguments []string `json:"arguments"`

	// EvidenceExcerpt is the doc's raw "## Import" section, verbatim.
	EvidenceExcerpt string `json:"evidence_excerpt"`

	// ExampleArguments are the arguments the doc's own "## Example Usage"
	// sets on a resource of this type to a self-contained literal, with
	// every cross-resource reference dropped. Issue #136: this is the
	// provenance that replaces tools/estate-gen's hand overrides for the
	// enum-member and policy-document cases - "the provider's own
	// documented example sets this", rechecked on every regeneration.
	ExampleArguments []ExampleArgument `json:"example_arguments,omitempty"`

	// ArgumentReference is the resource's top-level Argument Reference
	// bullets - name, Required/Optional and ForceNew where the doc marks
	// it - scraped from the same doc page's own "## Argument Reference"
	// section already read (via argumentReferenceNames) to produce
	// Arguments above. Widens the scrape past the Import section alone
	// (issue: the decisive identity evidence for many types sits in the
	// Argument Reference or the Identity Schema, not the Import section's
	// own prose). Additive: every existing reader of this file that only
	// looks at the fields above is unaffected by this field's presence.
	ArgumentReference []ArgumentRefEntry `json:"argument_reference,omitempty"`

	// ArgumentsInOrder is Arguments' own left-to-right order as the doc's
	// format-string prose states it, when classifyGrammar's format-token
	// signal resolved every segment to exactly one Argument Reference name
	// unambiguously - see composedResult.ArgumentsInOrder's own doc
	// comment. Empty whenever that order could not be recovered.
	ArgumentsInOrder []string `json:"arguments_in_doc_order,omitempty"`

	// IdentitySchemaRequired and IdentitySchemaOptional are the provider's
	// own Terraform 1.12+ "### Identity Schema" block's attribute names,
	// verbatim - recorded whenever the block exists, whether or not every
	// name resolves to a real configuration argument (unlike Arguments,
	// which is cross-checked against Argument Reference and populated only
	// when it does). A required identity attribute that is NOT a
	// configuration argument (aws_batch_compute_environment's "arn",
	// present only in Attribute Reference) is exactly the shape a
	// server-assigned type's real identity attribute needs, which Composed
	// and Arguments alone cannot express - they only ever answer the
	// composed-of-arguments question, not name the exported attribute
	// itself.
	IdentitySchemaRequired []string `json:"identity_schema_required,omitempty"`
	IdentitySchemaOptional []string `json:"identity_schema_optional,omitempty"`

	// IDTemplate is the documented import ID decomposed as a template
	// (issue #172): when the example is a full ARN or https:// URL, its
	// per-segment structure - scheme/partition/service literals, the
	// positional region and account slots, and the resource tail's
	// segments, each attributed to a configuration argument or recorded
	// Unattributed when no doc signal links them. Nil for the ordinary
	// short-ID example. See idtemplate.go.
	IDTemplate *IDTemplate `json:"import_id_template,omitempty"`

	// OmittedFallbacks records the Import section's own statement that one
	// of Arguments may be omitted from the composite ID and what the
	// service substitutes when it is: "(if you omit `event_bus_name`, the
	// `default` event bus will be used)" becomes
	// {"event_bus_name": "default"}. Scraped only from that documented
	// shape, and only for an argument Arguments already names - the
	// fallback literal is the doc's own backticked word, never a guess.
	// Three pages in the 6.59.0 cache carry the sentence, all EventBridge,
	// all "default"; the field exists so the identity table's
	// literal-fallback vocabulary (Component.Default) derives from the
	// doc's statement rather than from a hand ruling per type.
	OmittedFallbacks map[string]string `json:"omitted_fallbacks,omitempty"`

	// ExclusiveGroups are the mutually exclusive argument groups the doc
	// itself declares exactly one of must be supplied - aws_route's
	// "~> Exactly one of `destination_cidr_block`,
	// `destination_ipv6_cidr_block`, or `destination_prefix_list_id` is
	// required" - each group sorted, the groups themselves sorted, nil when
	// the page states none. This is the doc-stated source for the identity
	// table's preference-list Component.Attrs shape ("whichever of these the
	// configuration supplies"), which until now went into ratified rows by
	// hand because nothing in the scrape carried it. See exclusive.go for
	// the two spans read and the wider nets deliberately not cast.
	ExclusiveGroups [][]string `json:"exclusive_groups,omitempty"`

	// IDParts is the documented import ID's per-segment source attribution
	// (issue #132): each segment name the Import section's own prose gives,
	// attributed to the doc section that defines it - an Argument Reference
	// entry (configuration supplies it) or an Attribute Reference entry
	// (the server supplies it; the doc says so itself). Present only when
	// the prose names every segment of the documented example under the
	// resolved separator - see idParts. This is what distinguishes
	// aws_connect_queue's "instance_id:queue_id" (queue_id is an exported
	// attribute; the ID is not reconstructible from configuration) from
	// aws_lambda_alias's structurally identical-looking "function_name/
	// alias" (alias is prose shorthand for the `name` argument, which
	// resolves to neither section and stays "unknown").
	IDParts []IDPart `json:"id_parts,omitempty"`
}

// Counts are the sweep-wide totals a reviewer reads off the header without
// scanning every row.
type Counts struct {
	// TypesConsidered is how many TF resource types the roster named.
	TypesConsidered int `json:"types_considered"`
	// DocsFound / DocsMissing split TypesConsidered by whether the
	// provider publishes a website/docs/r/<name> page for it at all (a
	// 404 is expected for TF-side aliases like aws_alb, documented once
	// under their canonical name).
	DocsFound   int `json:"docs_found"`
	DocsMissing int `json:"docs_missing"`
	// NoImportSection is DocsFound minus the docs that produced a row.
	// Since issue #177 a page without a parseable "## Import" section
	// still rows when its Example Usage or Argument Reference said
	// anything (the row's grammar fields stay empty), so this counts only
	// the pages that stated nothing this tool extracts.
	NoImportSection int `json:"no_import_section"`
	// Rows is how many types produced a row - DocsFound minus
	// NoImportSection.
	Rows int `json:"rows"`

	// ComposedTrue / ComposedFalse / ComposedUnsure partition Rows by
	// ComposedOfArguments.
	ComposedTrue   int `json:"composed_true"`
	ComposedFalse  int `json:"composed_false"`
	ComposedUnsure int `json:"composed_unsure"`

	// SeparatorKnown / SeparatorUnsure partition Rows by whether Separator
	// is non-null.
	SeparatorKnown  int `json:"separator_known"`
	SeparatorUnsure int `json:"separator_unsure"`
}

// Artifact is the committed file: live/import-grammar.json.
type Artifact struct {
	// Provider and ProviderVersion pin the release every row's doc was
	// fetched at, the same vocabulary tools/survey-gen's Survey and
	// tools/mapping-gen's mapping.json use.
	Provider        string `json:"provider"`
	ProviderVersion string `json:"provider_version"`

	// GeneratedBy names the tool so a reader of the JSON alone knows where
	// rows come from and how to refresh them.
	GeneratedBy string `json:"generated_by"`

	// Accepted is the ISO date a human ran the generator with -accept and
	// ratified the rows below (issue #37 increment 1's vocabulary,
	// tools/registry-gen/pin.go's SpecPin.Accepted and
	// tools/survey-gen's own Accepted field). Empty unless -accept was
	// passed on the run that produced this file.
	Accepted string `json:"accepted,omitempty"`

	Counts Counts `json:"counts"`

	// Rows has one entry per TF type a doc's Import section was
	// successfully parsed for, sorted by TFType.
	Rows []Row `json:"rows"`
}

// buildRow parses one type's doc into a Row. ok is false when the doc has
// no "## Import" section, or has one but no literal ID example could be
// found in it - both cases the sweep counts as NoImportSection rather than
// emitting a row with nothing in it.
func buildRow(tfType, doc string) (Row, bool) {
	section, ok := importSection(doc)
	if !ok {
		// A page with no Import section still gets a row when its Example
		// Usage extraction found anything (issue #177): the example is a
		// statement about the type's arguments, and it was being discarded
		// with the whole row for the accident that the page ends before an
		// Import heading (aws_codegurureviewer_repository_association's
		// ends at Timeouts). Every import-grammar field stays empty - the
		// row claims nothing about a grammar the doc does not state.
		return exampleOnlyRow(tfType, doc)
	}
	idExample, ok := importIDExample(section)
	if !ok {
		// Same as above: an Import section this parser cannot pull an
		// example ID from settles no grammar, but it does not unsay the
		// page's Example Usage.
		return exampleOnlyRow(tfType, doc)
	}
	argNames := argumentReferenceNames(doc)
	argEntries := argumentReferenceEntries(doc)
	attrNames := attributeReferenceNames(doc)
	cr := classifyGrammar(section, argNames)
	idReq, _ := identitySchemaRequired(section)
	idOpt := identitySchemaOptional(section)

	// The enumeration idiom (issue #132): "using `app_id` and
	// `branch_name`" names every segment in order but states no separator,
	// so the clause and format-token signals both miss it. When every
	// enumerated token is a Required argument and nothing already resolved
	// disagrees, the enumeration is both the argument set and the doc's own
	// order.
	if cr.ArgumentsInOrder == nil && (cr.Composed == nil || *cr.Composed) {
		if order, ok := enumeratedRequiredArguments(section, argEntries); ok && subsetOf(cr.Arguments, order) {
			t := true
			cr.Composed = &t
			args := append([]string(nil), order...)
			sort.Strings(args)
			cr.Arguments = args
			cr.ArgumentsInOrder = order
		}
	}

	// The plain-prose family (issue #132): docs whose sentence names the
	// one argument the ID is without the backticked spelling every signal
	// in classifyGrammar keys on ("using repository name", "using the
	// AppSync API ID", "using the `name`" against image_name). Only ever
	// fills a gap the structured signals left - Composed still unresolved -
	// and only lands on names the Argument Reference confirms.
	proseArg := false
	if cr.Composed == nil {
		if arg, ok := proseNamedArgument(section, argEntries, attrNames); ok {
			t := true
			cr.Composed = &t
			cr.Arguments = []string{arg}
			proseArg = true
		}
	}

	// Last: the row's own documented example, when the prose settled
	// nothing (issue #135). Only ever fills a gap - a separator the doc
	// stated, or one classifyGrammar inferred from a format token, wins,
	// because both are statements about the ID's grammar while this is an
	// observation of one value. When the prose named the whole ID as one
	// argument (proseArg), the example-derived separator guess is skipped
	// entirely: the doc just said the ID is a single value, so punctuation
	// inside the example is internal to that value
	// (aws_cognito_identity_pool_roles_attachment's "us-west-2:b64805ad-…"
	// is one identity_pool_id, not two colon-joined parts) - a statement
	// about the grammar outranks an observation of one value, the same
	// hierarchy issue #135 established.
	if cr.Separator == nil {
		if sep, ok := separatorFromProse(section); ok {
			cr.Separator = &sep
		} else if sep, ok := separatorFromExample(idExample); ok && !proseArg {
			cr.Separator = &sep
		}
	}

	// The identity-block order proof (issue #132): the doc's own two
	// example forms, the 1.12+ identity block and the legacy plain id,
	// carry the same literal values - when joining the block's values in
	// its own order reproduces the plain id exactly, that order is proven,
	// not matched. Last of the order sources because it needs the resolved
	// separator, and only ever fills a gap the others left.
	if cr.ArgumentsInOrder == nil && cr.Composed != nil && *cr.Composed &&
		len(cr.Arguments) >= 2 && cr.Separator != nil {
		if order, ok := identityBlockOrder(section, *cr.Separator, argNames); ok && sameSet(order, cr.Arguments) {
			cr.ArgumentsInOrder = order
		}
	}
	parts := idParts(section, tfType, cr.Separator, idExample, argEntries, attrNames)
	exArgs := exampleArguments(doc, tfType)
	return Row{
		TFType:                 tfType,
		ImportIDExample:        idExample,
		Separator:              cr.Separator,
		ComposedOfArguments:    cr.Composed,
		Arguments:              cr.Arguments,
		EvidenceExcerpt:        section,
		ArgumentReference:      argEntries,
		ExampleArguments:       exArgs,
		ArgumentsInOrder:       cr.ArgumentsInOrder,
		IdentitySchemaRequired: idReq,
		IdentitySchemaOptional: idOpt,
		OmittedFallbacks:       omittedFallbacks(section, cr.Arguments),
		ExclusiveGroups:        exclusiveGroups(section, argEntries, doc),
		IDParts:                parts,
		IDTemplate:             idTemplate(section, tfType, argEntries, exArgs),
	}, true
}

// exampleOnlyRow is buildRow's no-Import-grammar path (issue #177): the
// row exists to carry the Example Usage extraction and the Argument
// Reference scrape, both statements the page makes regardless of whether
// it documents an import at all. ok is false only when neither found
// anything, which keeps every page that used to produce no row and states
// nothing new producing no row.
func exampleOnlyRow(tfType, doc string) (Row, bool) {
	exArgs := exampleArguments(doc, tfType)
	argEntries := argumentReferenceEntries(doc)
	if len(exArgs) == 0 && len(argEntries) == 0 {
		return Row{}, false
	}
	return Row{
		TFType:            tfType,
		ArgumentReference: argEntries,
		ExampleArguments:  exArgs,
	}, true
}

// tallyRow folds one Row into Counts.
func tallyRow(c *Counts, r Row) {
	c.Rows++
	switch {
	case r.ComposedOfArguments == nil:
		c.ComposedUnsure++
	case *r.ComposedOfArguments:
		c.ComposedTrue++
	default:
		c.ComposedFalse++
	}
	if r.Separator != nil {
		c.SeparatorKnown++
	} else {
		c.SeparatorUnsure++
	}
}

// buildArtifact sorts rows by type name and computes the header counts.
func buildArtifact(rows []Row, typesConsidered, docsFound, docsMissing int) Artifact {
	sort.Slice(rows, func(i, j int) bool { return rows[i].TFType < rows[j].TFType })

	art := Artifact{
		Provider:        providerSource,
		ProviderVersion: providerVersion,
		GeneratedBy:     "tools/importdocs-gen (go run ./tools/importdocs-gen)",
		Rows:            rows,
	}
	art.Counts.TypesConsidered = typesConsidered
	art.Counts.DocsFound = docsFound
	art.Counts.DocsMissing = docsMissing
	art.Counts.NoImportSection = docsFound - len(rows)
	for _, r := range rows {
		tallyRow(&art.Counts, r)
	}
	return art
}

// marshal renders the artifact deterministically: two-space indent,
// trailing newline, no HTML escaping - the same shape every other live/*
// generator's marshal uses.
func (a Artifact) marshal() ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(a); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
