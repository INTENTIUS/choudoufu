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

// This file is issue #331's derived fix: a resource identity schema, or even
// taggability, is not proof a migrate will actually IMPORT a type. Six types
// in the identity_schema_wire_only bucket carry a wire identity schema and
// no documented Import section; the audit that opened this issue found two
// of the six have no classic Importer at all
// (aws_iam_policy_attachment, aws_acm_certificate_validation) and would
// hard-fail "resource ... doesn't support import" the instant a real migrate
// calls ImportResourceState - internal/legacy/helper/schema/provider.go's
// Provider.ImportState returns that error the moment Importer == nil, before
// any API call. A full sweep of the provider's whole roster found the same
// gap reaches further and by a different route: aws_iot_ca_certificate and
// aws_lightsail_domain are both admitted today - one by a ratified table row
// on registry evidence, one by the untaggable-but-enumerable path - and
// neither has an Importer either.
//
// The rule is derived, per this generator's standing bar - it names no
// resource type (see notImportableExempt below for the one documented
// exception). Importability comes from live/survey-full.json's own
// signals.importable, tools/survey-gen's ImportResourceState probe
// (schemas.go's probeImportability): one extra RPC per type, over the same
// provider connection the schema read already launches, with a
// syntactically invalid dummy ID that never reaches AWS either way.
//
// notImportableRoster feeds emittedRows' vetoed set exactly the way
// markerlessRoster does (issue #249's precedent): a vetoed type gets no row
// in [identity.DefaultTable] at all, whether or not a ratification batch had
// already written one, because leaving the row in place would promise
// support a real migrate cannot deliver. See emit.go's own comment on
// emittedRows for why retraction has to reach backwards this way rather than
// only forwards.

// notImportableReason is the whole ruling, stated once - the same shape
// markerlessReason takes, and for the same reason: this is one rule against
// the whole roster, not 68 separate judgments.
const notImportableReason = "tools/survey-gen's ImportResourceState probe found no classic Importer for this type - a live-import would " +
	"fail \"resource ... doesn't support import\" (or the plugin framework's \"Resource Import Not Implemented\") the moment a real " +
	"migrate calls it, regardless of what the resource identity schema or taggability otherwise promise (issue #331)"

// notImportableExempt is the one hand-written input this file's derivation
// carries, the same shape tools/survey-gen/classify.go's opsExcluded is: a
// ruling that genuinely cannot be derived from the importable signal alone,
// recorded here with its own evidence rather than folded into the rule.
//
// aws_acm_certificate_validation is importable=false by the same probe as
// every other entry in this file - it has no classic Importer either - but
// its classification is a separate, already-ruled maintainer decision on a
// different axis. classify.go's 2026-08-17 ruling withdrew it from
// opsExcluded because identity.Derivable resolves its identity
// (certificate_arn) straight from configuration, with no discovery and no
// Importer ever in that path: the type is admitted on NAMEABILITY, not on
// whether ImportResourceState succeeds. Issue #331's audit surfaced the
// importability gap as new evidence on that second axis, and said in its own
// words that reversing the nameability ruling over it is the maintainer's
// call, not a mechanical veto tied to this fix. So this file records the gap
// truthfully in live/survey-full.json's signal and does not act on it for
// this one type.
var notImportableExempt = map[string]string{
	"aws_acm_certificate_validation": "admitted on the nameability axis by classify.go's 2026-08-17 ruling (identity.Derivable " +
		"resolves certificate_arn from configuration with no Importer in that path); issue #331's importability finding is new " +
		"evidence on a different axis, and reversing the ruling over it is the maintainer's call",
}

// notImportableRoster is every provider type the rule vetoes, sorted.
//
// It iterates live/survey-full.json's own entries, the same precondition
// markerlessRoster's doc comment states and for the same reason: a type the
// survey does not cover can never be vetoed by the absence of a signal, since
// a missing entry would decode as importable=false and reading that as
// evidence would veto on silence rather than on a measurement.
func notImportableRoster(survey map[string]surveyEntry) []string {
	var out []string
	for typeName, entry := range survey {
		if entry.Signals.Importable {
			continue
		}
		if _, exempt := notImportableExempt[typeName]; exempt {
			continue
		}
		out = append(out, typeName)
	}
	sort.Strings(out)
	return out
}

// notImportableTableRel is the generated roster's home, beside
// [identity.MarkerlessTypes] for the same reason that one sits beside the
// identity table: it answers the same question - may this type be admitted -
// and internal/live's own accounting has to read it to know a type outside
// the identity table is outside it for a recorded reason.
const notImportableTableRel = "internal/live/identity/notimportable_generated.go"

// renderNotImportableFile renders internal/live/identity's not-importable
// veto roster, the same shape renderMarkerlessFile takes.
func renderNotImportableFile(types []string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(licenseHeader)
	b.WriteString("\n")
	b.WriteString(emitGeneratedByComment)
	b.WriteString("\n\n")
	b.WriteString("package identity\n\n")
	b.WriteString(notImportableDoc)
	fmt.Fprintf(&b, "const NotImportableReason = %q\n\n", notImportableReason)
	b.WriteString(notImportableTypesDoc)
	b.WriteString("var NotImportableTypes = map[string]struct{}{\n")
	for _, t := range types {
		fmt.Fprintf(&b, "%q: {},\n", t)
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}

// notImportableDoc is NotImportableReason's own doc comment.
const notImportableDoc = `// NotImportableReason is why every type in [NotImportableTypes] is refused
// admission regardless of what its identity schema or taggability otherwise
// promise. It is one ruling covering the whole set, not a summary of many.
`

// notImportableTypesDoc is NotImportableTypes' own doc comment.
const notImportableTypesDoc = `// NotImportableTypes is every provider resource type tools/row-gen refuses
// to admit because the provider reports no classic Importer for it - see
// [NotImportableReason]. A wire identity schema, or even taggability, is not
// proof a real migrate can import the type (issue #331): this is the one
// signal that asks the question directly, tools/survey-gen's
// ImportResourceState probe.
//
// This is a veto, not a gap, the same distinction [MarkerlessTypes] draws:
// a type here is absent from [DefaultTable] deliberately and by a derived
// rule. The set is derived on every generator run from live/survey-full.json's
// importable signal, with exactly one named, evidenced exception recorded in
// tools/row-gen/notimportable.go's own notImportableExempt - nothing here is
// otherwise maintained by hand.
`
