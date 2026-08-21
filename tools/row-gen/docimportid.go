// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"go/format"
	"sort"
	"strings"
)

// This file is GitHub issue #337's second half.
//
// The first half (idnotwhole.go) turned "nothing proves this type's exported
// `id` is the whole documented import string" into a refusal. That was the
// right first move - a refusal is visible and a wrong record is not - but a
// refusal is not a fix. #337 asks for the other direction as well: where the
// provider's own documentation states the import string's grammar, read it
// and record the whole string, so the type is admitted rather than refused.
//
// # The gap this fills, stated as the gate it changes
//
// [identity.LocatedIdentityPlanFor] qualifies a type two ways today. The
// provider's wire identity schema settles it when the schema requires `id`
// plus at least one other attribute (issue #329): the record then carries the
// identity OBJECT, and no order and no separator is ever guessed because an
// identity object has neither. Otherwise the record carries the bare string
// `id`, which is a bet, and [identity.IDNotProvenWholeTypes] refuses the bet
// where the page documents a composite import.
//
// Measured at 56481a4bbf, 42 of the markerless population's 59
// composite-import types carry no wire identity schema at all, so for those
// the wire gate is not a gate - it is silence. This file adds the second
// qualification route for exactly them: the Import section's own scraped
// segment attribution.
//
// # Why reading an ORDER here is sound where guessing one is not
//
// Issue #105's rule is that a composite identity must never be flattened into
// a plausible string, and #329 kept it by construction: identity-schema
// attributes are unordered, so there was nothing to flatten and no separator
// to invent.
//
// Nothing is invented here either, and the reason is that the fact being read
// is a different one. An identity OBJECT has no string form; a documented
// IMPORT ID is a string by definition, and the page states it - the separator
// in live/import-grammar.json's own scraped Separator, the segment order in
// tools/importdocs-gen's IDParts, which is the Import section's own prose
// naming its segments in the order the documented example puts them. IDParts
// carries its own arity discipline: importdocs-gen returns nothing unless the
// named segments account for EVERY segment of the documented example under
// the resolved separator, so a partial name list never reaches here. That is
// the difference between reading a grammar and inventing one.
//
// # What this file will not read
//
// A segment the page names with a prose PHRASE rather than a token - "the API
// mapping identifier", "their EMR Cluster id", "partition values" - is
// refused, and the rule is the shape of the token and not a list of the
// phrases: a name is a single word, and a multi-word phrase is a description
// of a value rather than the name of one. Nothing here tries to match a
// phrase to an argument, because the match would be a guess and a guessed
// segment is a wrong identity - the failure mode this whole mechanism exists
// to avoid.
//
// The other half of the reading is deliberately NOT done here, because it
// cannot be: whether a named segment is actually an attribute the applied
// object carries is a question about the provider's schema, which this
// generator does not hold. So this file emits a PROPOSAL - ordered names and
// a separator - and internal/live/identity corroborates every name against
// the real schema at run time, refusing the type if any name is not there.
// Generator reads the documentation; run time checks it against the provider.
//
// It names no resource type, here or in anything it reads.

// docImportIDTableRel is the generated grammar's home, beside
// idnotwhole_generated.go for the reason that file's own constant states.
const docImportIDTableRel = "internal/live/identity/docimportid_generated.go"

// docImportIDRow is one type's documented import-ID grammar.
type docImportIDRow struct {
	// TFType is the provider resource type.
	TFType string

	// Separator is live/import-grammar.json's own scraped separator.
	Separator string

	// Parts are the documented segments in the documented order.
	Parts []docImportIDPart
}

// docImportIDPart is one documented segment.
type docImportIDPart struct {
	// Name is the segment's name reduced to [normalizeName]'s comparison
	// form, so the consumer can match it against schema attribute names
	// without re-implementing the reduction.
	Name string

	// Argument records whether the page's own Argument Reference names
	// this segment - tools/importdocs-gen's [idPartSourceArgument]. It is
	// carried for one decision and no other: the consumer may treat ONE
	// unresolvable segment as the resource's server-minted `id`, and a
	// segment the page calls a configuration argument is not that. Without
	// this field, a segment the page names and the schema spells
	// differently would be silently read as the minted leaf, which puts
	// the right value in the wrong position - a wrong identity, composed
	// confidently.
	Argument bool
}

// docImportIDRoster reads every scraped row's documented import grammar and
// keeps the ones a consumer could act on.
//
// grammar is live/import-grammar.json indexed by TF type, passed in rather
// than read here for the reason every other roster in this package states.
//
// # Why the population is every row and not the markerless ones
//
// idnotwhole.go's own doc comment gives the reason and it applies unchanged:
// this generator's -emit run REWRITES [identity.MarkerlessTypes], so a set
// derived from it would be derived from the previous run's answer. "What does
// this page say the import string is made of" is a question about one
// documentation page, and the answer does not change with whether the type
// happens to be vetoed.
//
// # Why the whole-`id` types are excluded
//
// A type classifyCompositeImport calls [compositeVerdictWhole] has an
// Attribute Reference stating that `id` itself holds the whole composite
// string. For such a type the existing bare-`id` rule is already correct and
// already applies, and composing the string a second time out of its segments
// would produce the same string at best and a doubled one at worst. So the
// population here is exactly the population the bare-`id` rule refuses -
// [identity.IDNotProvenWholeTypes] - which makes this route incapable of
// changing any verdict that works today. It can only turn a refusal into an
// admission.
func docImportIDRoster(grammar map[string]importGrammarRow) []docImportIDRow {
	var out []docImportIDRow
	for typeName, g := range grammar {
		if g.Separator == nil || *g.Separator == "" {
			// A flat documented import has no segments to order.
			continue
		}
		if classifyCompositeImport(typeName, g).Verdict == compositeVerdictWhole {
			// The page already states `id` is the whole string; see this
			// function's own doc comment.
			continue
		}
		parts, ok := docImportIDParts(g)
		if !ok {
			continue
		}
		out = append(out, docImportIDRow{TFType: typeName, Separator: *g.Separator, Parts: parts})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TFType < out[j].TFType })
	return out
}

// docImportIDParts reduces one row's scraped IDParts to the comparison-form
// segment names a consumer can match against a schema, or refuses.
//
// Three requirements, each of them a property of the scrape rather than of
// any particular type:
//
//  1. At least two segments. One segment is not a composite, and the
//     separator would join nothing.
//
//  2. Every segment is named by a single token. importdocs-gen attributes a
//     segment to "argument", "attribute", "own-id" or "unknown", and that
//     attribution is not what decides here - the SHAPE of the name is.
//     "policy_store_id" and "REST-API-ID" are names of things; "the API
//     mapping identifier" is a description of one, and turning a description
//     into a name is the guess this refuses to make.
//
//  3. The reduced names are distinct. Two segments reducing to one name means
//     the reduction has lost the difference between them, and a consumer
//     matching both against the same attribute would compose a string with
//     one segment repeated and another missing.
//
// The attribution decides nothing here - a segment importdocs-gen could not
// attribute is still a segment the page NAMED, and whether the name
// corresponds to something the applied object carries is a schema question,
// answered where the schema is. It is carried through for the consumer's own
// use; see [docImportIDPart.Argument].
func docImportIDParts(g importGrammarRow) ([]docImportIDPart, bool) {
	if len(g.IDParts) < 2 {
		return nil, false
	}
	parts := make([]docImportIDPart, 0, len(g.IDParts))
	seen := make(map[string]bool, len(g.IDParts))
	for _, p := range g.IDParts {
		if len(strings.Fields(p.Token)) != 1 {
			return nil, false
		}
		n := normalizeName(p.Token)
		if n == "" || seen[n] {
			return nil, false
		}
		seen[n] = true
		parts = append(parts, docImportIDPart{Name: n, Argument: p.Source == idPartSourceArgument})
	}
	return parts, true
}

// renderDocImportIDFile renders internal/live/identity's copy of the roster.
func renderDocImportIDFile(rows []docImportIDRow) ([]byte, error) {
	var b strings.Builder
	b.WriteString(licenseHeader)
	b.WriteString("\n")
	b.WriteString(emitGeneratedByComment)
	b.WriteString("\n\n")
	b.WriteString("package identity\n\n")
	b.WriteString(docImportIDDoc)
	b.WriteString("var DocumentedImportIDs = map[string]DocumentedImportID{\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%q: {Separator: %q, Parts: []DocumentedImportIDPart{", r.TFType, r.Separator)
		for i, p := range r.Parts {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "{Name: %q, Argument: %v}", p.Name, p.Argument)
		}
		b.WriteString("}},\n")
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}

// docImportIDDoc is DocumentedImportIDs' own doc comment, carried by the
// generator for the reason idNotWholeDoc is.
const docImportIDDoc = `// DocumentedImportIDs is every provider resource type whose Import section
// documents a COMPOSITE import string, whose exported ` + "`id`" + ` no source proves
// to be that whole string, and whose segments the page names one token at a
// time - the grammar, in the documented order, with the documented separator.
//
// It is issue #337's second half, and it is the second way a type qualifies
// for a composite located identity. [IDNotProvenWholeTypes] is the first
// half: it refuses a type whose ` + "`id`" + ` might be a fragment. A type here is one
// the same page goes on to describe well enough to COMPOSE the whole string
// from the applied object, so [LocatedIdentityPlanFor] admits it instead of
// refusing it.
//
// # What Parts holds, and what it does not
//
// Parts are segment names in the documented segment order, reduced to the
// comparison form a name match uses (lower case, punctuation removed), so
// "REST-API-ID" and "rest_api_id" are one name. They are names read off a
// documentation page and nothing has checked them against a provider: a name
// here may correspond to no attribute at all. [LocatedIdentityPlanFor] is
// where every name is resolved against the real schema, and a name that
// resolves to nothing is what makes the type refused rather than admitted.
//
// # Why an ORDER is safe to carry here
//
// Issue #105 forbids flattening a composite IDENTITY into a plausible string,
// and #329 kept that rule by having nothing to flatten. This is a different
// fact: a documented import ID is a string, and the page states both its
// separator and its segment order. tools/importdocs-gen returns segments only
// when the names it read account for every segment of the documented example
// under that separator, so a partial reading never becomes an order.
//
// The set is derived on every generator run from live/import-grammar.json.
// Nothing here is maintained by hand.
`
