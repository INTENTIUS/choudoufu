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

// This file carries issue #337's verdict into the package that acts on it.
//
// compositeimport.go computes the verdict and writes it as a REPORT
// (live/composite-import-roster.json) over the markerless population, for a
// reader. internal/live/identity needs the same verdict as a FACT it can
// consult at run time, because [identity.LocatedIdentityComponents] is where
// recording the wrong string actually happens. So the rule is written once,
// in classifyCompositeImport, and rendered twice.
//
// # Why this set is computed over every type and not over the markerless ones
//
// The report's population is [identity.MarkerlessTypes], which is the right
// population for a reader asking "what is left to prove". It is the wrong
// one for a generated Go fact, for a mechanical reason: this generator's own
// -emit run REWRITES MarkerlessTypes, so a set derived from it would be
// derived from the previous run's answer. Issue #263 is what that failure
// mode costs when nobody notices.
//
// "Is this type's exported `id` the whole string its Import section
// documents" is a question about one documentation page, and the answer does
// not change with whether the type happens to be vetoed. Asking it of every
// row in live/import-grammar.json makes the set order-independent, and any
// type that later enters the veto is already covered rather than covered one
// generator run later.
//
// # What membership does and does not mean
//
// Membership is "unproven", never "proven wrong". A type is in this set when
// its Import section documents a composite string and no independent source
// corroborates that the exported `id` is that whole string. Some members are
// certainly leaves and some are certainly whole; nothing here claims to know
// which, and the consumer treats the whole set the same way, by refusing.
// That is the conservative direction and it is the only one available: the
// alternative is a record written from a guess.
//
// A type with NO grammar row, or one whose documented import is flat, is
// absent from this set and keeps today's rule. That is not an oversight and
// it is worth stating: for a flat import, `id` being the whole string is what
// "flat" means, and for a type with no scraped Import section at all there is
// no composite claim to fail to corroborate. The residue this leaves is
// therefore types whose docs describe no import at all, which is a scrape
// gap and not a verdict.
//
// It names no resource type, here or in anything it reads.

// idNotWholeTableRel is the generated set's home, beside the identity table
// and markerless_generated.go for the reason markerlessTableRel states: the
// package that acts on a ruling is where the ruling's evidence should be
// readable from.
const idNotWholeTableRel = "internal/live/identity/idnotwhole_generated.go"

// idNotProvenWholeRoster is every type in the scraped import grammar whose
// documented import string is composite and whose exported `id` no source
// proves to be that whole string, sorted.
//
// grammar is live/import-grammar.json indexed by TF type, passed in rather
// than read here for the reason every other roster in this package states.
func idNotProvenWholeRoster(grammar map[string]importGrammarRow) []string {
	var out []string
	for typeName, g := range grammar {
		if g.Separator == nil || *g.Separator == "" {
			// A flat documented import. `id` being the whole string is
			// what flat means.
			continue
		}
		if classifyCompositeImport(typeName, g).Verdict == compositeVerdictWhole {
			continue
		}
		out = append(out, typeName)
	}
	sort.Strings(out)
	return out
}

// renderIDNotWholeFile renders internal/live/identity's copy of the roster.
func renderIDNotWholeFile(types []string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(licenseHeader)
	b.WriteString("\n")
	b.WriteString(emitGeneratedByComment)
	b.WriteString("\n\n")
	b.WriteString("package identity\n\n")
	b.WriteString(idNotWholeDoc)
	b.WriteString("var IDNotProvenWholeTypes = map[string]struct{}{\n")
	for _, t := range types {
		fmt.Fprintf(&b, "%q: {},\n", t)
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}

// idNotWholeDoc is IDNotProvenWholeTypes' own doc comment, carried by the
// generator for the reason markerlessTypesDoc is.
const idNotWholeDoc = `// IDNotProvenWholeTypes is every provider resource type whose Import section
// documents a COMPOSITE import string and for which no independent source
// proves the exported ` + "`id`" + ` attribute holds that whole string.
//
// It is issue #337's verdict, in the form the record-located mechanism can
// act on. [LocatedIdentityComponents] records a type's ` + "`id`" + ` and hands that
// string back to import on the next run, which is correct only when ` + "`id`" + ` IS
// the import string. For a type here nothing says it is, so the mechanism
// refuses rather than recording a possible fragment - a recorded fragment is
// a wrong identity, and a wrong identity is invisible to every verdict-level
// check until the NEXT run's import fails.
//
// Membership is UNPROVEN, not disproven. The rule is the Import section's
// own scraped separator and the Attribute Reference's ` + "`id`" + ` bullet naming the
// same join character - two sections of one page, different purposes,
// different scrapers. A type whose page states no composite ` + "`id`" + ` form, or
// documents no ` + "`id`" + ` attribute at all, is here because its page does not
// settle the question, not because the answer is known to be no.
//
// A type whose documented import is FLAT is absent: ` + "`id`" + ` being the whole
// string is what flat means. So is a type with no scraped Import section,
// which makes no composite claim for anything to corroborate.
//
// The set is derived on every generator run from live/import-grammar.json.
// Nothing here is maintained by hand. live/composite-import-roster.json is
// the same rule rendered as a report over the markerless population, with
// the per-type evidence and the reason each unproven type is unproven.
`
