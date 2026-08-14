// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is GitHub issue #106's first acceptance criterion: a generated
// artifact reporting, per type, where the sources describing a resource's
// identity disagree.
//
// # What the artifact found, which is not what the issue expected
//
// The issue frames this as three sources that might each disagree with the
// other two. Measured, two of them never do. The provider's own identity
// schema (live/survey-full.json's required_for_import, read off a live
// GetProviderSchema boot) and the scraped documentation
// (live/import-grammar.json's identity_schema_required) cover 438 types
// between them and agree on every single one. They are the same fact
// reaching the repository two ways, and the scrape is faithful.
//
// So the artifact reports that agreement rather than burying it, and puts
// its weight where the disagreement actually is: between those two and what
// this tool derives from the CloudFormation Registry. That is the axis
// aws_ecs_service's phantom service_arn sits on, and the one crosscheck.go
// now refuses on.
//
// Coverage gaps are reported too, and are a different thing from conflicts:
// a type one source describes and the other does not is a hole in the
// evidence, not a contradiction in it.

// sourcesArtifactPath is where the report is written, relative to the
// repository root.
const sourcesArtifactPath = "live/identity-sources.json"

// sourcesArtifact is the whole report.
type sourcesArtifact struct {
	GeneratedBy string        `json:"generated_by"`
	Summary     sourcesTotals `json:"summary"`

	// Conflicts are the types where two sources describe the identity and
	// describe it differently. This is the list that matters.
	Conflicts []sourceConflict `json:"conflicts"`

	// WireOnly and DocsOnly are coverage gaps: one source has an identity
	// schema for the type and the other has none. Not a disagreement.
	WireOnly []string `json:"identity_schema_wire_only"`
	DocsOnly []string `json:"identity_schema_docs_only"`
}

type sourcesTotals struct {
	// Wire and Docs are how many types each source describes.
	Wire int `json:"wire_identity_schemas"`
	Docs int `json:"docs_identity_schemas"`

	// Both is how many types both describe, and Agree how many of those
	// they agree on. The interesting number is Both-Agree.
	Both  int `json:"described_by_both"`
	Agree int `json:"agree"`

	// TableRows is how many ratified rows were compared, and
	// TableNamesUnknownArgument how many name an argument neither source
	// knows - the aws_ecs_service shape, in the table rather than in a
	// proposal.
	TableRows                 int `json:"table_rows_compared"`
	TableNamesUnknownArgument int `json:"table_rows_naming_an_unknown_argument"`
}

// sourceConflict is one type two sources describe differently.
type sourceConflict struct {
	Type string `json:"type"`

	// Kind is "wire-vs-docs" or "table-vs-schema".
	Kind string `json:"kind"`

	Wire  []string `json:"wire,omitempty"`
	Docs  []string `json:"docs,omitempty"`
	Table []string `json:"table,omitempty"`

	Detail string `json:"detail"`
}

// runSources builds the report and writes it.
func runSources(out, errOut *os.File) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		return err
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		return err
	}

	art := buildSourcesArtifact(survey, grammar)
	art.GeneratedBy = "tools/row-gen -sources"

	data, err := json.MarshalIndent(art, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	path := filepath.Join(root, sourcesArtifactPath)
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // a committed artifact, not a secret
		return err
	}

	fmt.Fprintf(errOut, "row-gen: wrote %s\n", sourcesArtifactPath)
	fmt.Fprintf(out, "identity sources: wire=%d docs=%d both=%d agree=%d conflicts=%d\n",
		art.Summary.Wire, art.Summary.Docs, art.Summary.Both, art.Summary.Agree, len(art.Conflicts))
	fmt.Fprintf(out, "ratified rows compared=%d, naming an argument no source knows=%d\n",
		art.Summary.TableRows, art.Summary.TableNamesUnknownArgument)
	return nil
}

// buildSourcesArtifact is runSources without the file handling, so a test
// can read the result.
func buildSourcesArtifact(survey map[string]surveyEntry, grammar map[string]importGrammarRow) sourcesArtifact {
	var art sourcesArtifact

	wire := map[string][]string{}
	for t, e := range survey {
		if e.Identity != nil && len(e.Identity.RequiredForImport) > 0 {
			wire[t] = sortedCopy(e.Identity.RequiredForImport)
		}
	}
	docs := map[string][]string{}
	for t, g := range grammar {
		if len(g.IdentitySchemaRequired) > 0 {
			docs[t] = sortedCopy(g.IdentitySchemaRequired)
		}
	}
	art.Summary.Wire = len(wire)
	art.Summary.Docs = len(docs)

	for _, t := range sortedKeysOf(wire) {
		d, both := docs[t]
		if !both {
			art.WireOnly = append(art.WireOnly, t)
			continue
		}
		art.Summary.Both++
		if equalStrings(wire[t], d) {
			art.Summary.Agree++
			continue
		}
		art.Conflicts = append(art.Conflicts, sourceConflict{
			Type: t, Kind: "wire-vs-docs", Wire: wire[t], Docs: d,
			Detail: "the provider's own identity schema and the scraped documentation describe this type's identity differently",
		})
	}
	for _, t := range sortedKeysOf(docs) {
		if _, both := wire[t]; !both {
			art.DocsOnly = append(art.DocsOnly, t)
		}
	}

	// The axis that actually carries disagreement: what the shipped table
	// says against what the two schema sources say. crosscheck.go refuses
	// this shape in a fresh proposal; this reports it in what already
	// shipped.
	for _, t := range sortedKeysOf(identity.DefaultTable) {
		entry := identity.DefaultTable[t]
		named := tableArgumentNames(entry)
		if len(named) == 0 {
			continue
		}
		g, hasGrammar := grammar[t]
		if !hasGrammar || len(g.ArgumentReference) == 0 || len(g.IdentitySchemaRequired) == 0 {
			continue
		}
		art.Summary.TableRows++

		documented := documentedArguments(g)
		required := identitySchemaAttrs(g)
		optional := namesOf(g.IdentitySchemaOptional)

		var unknown []string
		for _, name := range named {
			if documented[name] || required[name] || optional[name] {
				continue
			}
			unknown = append(unknown, name)
		}
		if len(unknown) == 0 {
			continue
		}
		art.Summary.TableNamesUnknownArgument++
		art.Conflicts = append(art.Conflicts, sourceConflict{
			Type: t, Kind: "table-vs-schema",
			Table: unknown, Wire: wire[t], Docs: docs[t],
			Detail: "the ratified table row builds this type's identity from an argument neither the documented Argument Reference nor the Identity Schema knows",
		})
	}

	sort.Slice(art.Conflicts, func(i, j int) bool {
		if art.Conflicts[i].Kind != art.Conflicts[j].Kind {
			return art.Conflicts[i].Kind < art.Conflicts[j].Kind
		}
		return art.Conflicts[i].Type < art.Conflicts[j].Type
	})
	return art
}

// tableArgumentNames is every argument name a ratified row reads, in sorted
// order. Literals and cloud values contribute nothing.
func tableArgumentNames(entry identity.TypeIdentity) []string {
	seen := map[string]bool{}
	for _, c := range entry.Components {
		for _, a := range c.Attrs {
			seen[a] = true
		}
	}
	return sortedKeysOf(seen)
}

func sortedCopy(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func sortedKeysOf[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
