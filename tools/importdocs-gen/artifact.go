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
	// NoImportSection is DocsFound minus the docs that carry a parseable
	// "## Import" section with a literal ID example (e.g. a waiter
	// resource with nothing to import).
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
		return Row{}, false
	}
	idExample, ok := importIDExample(section)
	if !ok {
		return Row{}, false
	}
	argNames := argumentReferenceNames(doc)
	cr := classifyGrammar(section, argNames)
	idReq, _ := identitySchemaRequired(section)
	idOpt := identitySchemaOptional(section)
	return Row{
		TFType:                 tfType,
		ImportIDExample:        idExample,
		Separator:              cr.Separator,
		ComposedOfArguments:    cr.Composed,
		Arguments:              cr.Arguments,
		EvidenceExcerpt:        section,
		ArgumentReference:      argumentReferenceEntries(doc),
		ArgumentsInOrder:       cr.ArgumentsInOrder,
		IdentitySchemaRequired: idReq,
		IdentitySchemaOptional: idOpt,
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
