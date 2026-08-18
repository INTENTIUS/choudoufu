// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"fmt"
	"go/format"
	"os"
	"sort"
	"strings"
)

// This file is issue #272's two-source admission rule: a type whose
// identity-bearing argument is proven unique within the account, by two
// independent sources agreeing, may be found by matching a live object's
// own property against that argument's declared value instead of reading
// an ownership marker off it - see [identity.ContentMatchBinding]'s doc
// comment for the mechanism this feeds and internal/live/discovery for the
// leg that consumes it.
//
// The two sources, and why neither alone is enough - the same discipline
// markerless.go's own two-source read applies for the veto path, the other
// direction:
//
//   - The provider's own Argument Reference
//     (tools/importdocs-gen's ArgumentRefEntry.DeclaredUnique, carried
//     through live/import-grammar.json). This is the source a user's
//     configuration is written against, but it is prose a doc author
//     wrote, and prose can overstate what the API actually enforces.
//   - The CloudFormation Registry's own property description
//     (tools/registry-gen's UniqueNameProperty.DeclaredUnique, carried
//     through live/registry-schema-facts.json). This is closer to the
//     API's own schema, but it describes CloudFormation's model of the
//     resource, which is not always the provider's.
//
// Requiring both to agree is what keeps aws_cloudfront_origin_access_control
// out: its provider docs say "A name that identifies the Origin Access
// Control" (no uniqueness claim) and its CFN registry description matches
// ("A name to identify the origin access control", no "unique" either) -
// so BOTH sources correctly say no, and the type stays refused. A type
// where only one source claims uniqueness is refused the same way a type
// where neither does is: this rule has no partial-credit path.

// schemaFactEntry is the slice of live/registry-schema-facts.json's
// per-type shape (tools/registry-gen's SchemaFact) this tool reads: just
// the resource-owned Name property, never the handler permissions or enum
// members that artifact also carries.
type schemaFactEntry struct {
	TypeName           string `json:"type_name"`
	UniqueNameProperty *struct {
		Path           []string `json:"path"`
		DeclaredUnique bool     `json:"declared_unique"`
	} `json:"unique_name_property,omitempty"`
}

type schemaFactsArtifact struct {
	Types []schemaFactEntry `json:"types"`
}

// loadSchemaFacts reads live/registry-schema-facts.json and indexes it by
// CFN type name. A missing file is not an error, the same way
// loadImportGrammar's is not: every artifact this tool already reads
// predates this one's addition to the pipeline, and a type this cannot
// speak for simply never qualifies for the content-match rule below.
func loadSchemaFacts(path string) (map[string]schemaFactEntry, error) {
	data, err := os.ReadFile(path) //nolint:gosec // a fixed path inside the checkout
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]schemaFactEntry{}, nil
		}
		return nil, err
	}
	var art schemaFactsArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		return nil, fmt.Errorf("decoding %s: %w", path, err)
	}
	out := make(map[string]schemaFactEntry, len(art.Types))
	for _, e := range art.Types {
		out[e.TypeName] = e
	}
	return out, nil
}

// contentMatchRoster is every provider type the two-source rule qualifies,
// sorted by TF type name. It reads every proposal regardless of bucket - a
// type's server-assignment or taggability says nothing about whether its
// name is proven unique, and internal/live/identity.MarkerlessTypes'
// bypass (markerless.go's contentMatch parameter) is what actually
// restricts the mechanism to the population that needs it. Computing the
// roster over the full mapped set instead keeps this file honest about
// what it derives: a fact about the type, not a decision about which types
// need the fact.
func contentMatchRoster(proposals []proposal, grammar map[string]importGrammarRow, facts map[string]schemaFactEntry) []contentMatchRow {
	var out []contentMatchRow
	for _, p := range proposals {
		if p.CFNType == "" {
			continue // a fold row with no CFN type of its own
		}
		fact, ok := facts[p.CFNType]
		if !ok || fact.UniqueNameProperty == nil || !fact.UniqueNameProperty.DeclaredUnique {
			continue
		}
		path := fact.UniqueNameProperty.Path
		if len(path) == 0 {
			continue
		}
		leaf := normalizeName(path[len(path)-1])

		g, ok := grammar[p.TFType]
		if !ok {
			continue
		}
		var argName string
		var argOK bool
		for _, a := range g.ArgumentReference {
			if normalizeName(a.Name) == leaf && a.DeclaredUnique {
				argName, argOK = a.Name, true
				break
			}
		}
		if !argOK {
			continue
		}

		out = append(out, contentMatchRow{
			TFType:       p.TFType,
			Argument:     argName,
			CFNType:      p.CFNType,
			PropertyPath: append([]string(nil), path...),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TFType < out[j].TFType })
	return out
}

// contentMatchRow is one qualifying type, in the shape
// [identity.ContentMatchBinding] mirrors.
type contentMatchRow struct {
	TFType       string
	Argument     string
	CFNType      string
	PropertyPath []string
}

// contentMatchSet indexes contentMatchRoster's rows by TF type, for
// markerlessRoster's bypass check.
func contentMatchSet(rows []contentMatchRow) map[string]bool {
	out := make(map[string]bool, len(rows))
	for _, r := range rows {
		out[r.TFType] = true
	}
	return out
}

// contentMatchTableRel is the generated roster's home, beside the identity
// table and the markerless roster for the same reason both of those sit
// there: internal/live/discovery's content-match leg reads it at run time.
const contentMatchTableRel = "internal/live/identity/contentmatch_generated.go"

// renderContentMatchFile renders internal/live/identity's content-match
// roster.
func renderContentMatchFile(rows []contentMatchRow) ([]byte, error) {
	var b strings.Builder
	b.WriteString(licenseHeader)
	b.WriteString("\n")
	b.WriteString(emitGeneratedByComment)
	b.WriteString("\n\n")
	b.WriteString("package identity\n\n")
	b.WriteString(contentMatchTypesDoc)
	b.WriteString("var ContentMatchTypes = map[string]ContentMatchBinding{\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%q: {\n", r.TFType)
		fmt.Fprintf(&b, "Argument: %q,\n", r.Argument)
		fmt.Fprintf(&b, "CFNType: %q,\n", r.CFNType)
		b.WriteString("PropertyPath: []string{")
		for i, seg := range r.PropertyPath {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%q", seg)
		}
		b.WriteString("},\n")
		b.WriteString("},\n")
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}

// contentMatchTypesDoc is ContentMatchTypes' own doc comment.
const contentMatchTypesDoc = `// ContentMatchTypes is every provider resource type issue #272's two-source
// rule qualifies for content-match discovery - see [ContentMatchBinding].
//
// The set is derived on every generator run from live/import-grammar.json's
// and live/registry-schema-facts.json's own DeclaredUnique signals, joined
// by the property name both sources describe. Nothing here is maintained by
// hand.
`
