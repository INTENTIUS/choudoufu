// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"go/format"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"

	"github.com/intentius/choudoufu/internal/live/identity"
)

// This file is -emit's whole implementation (issue #96): where every other
// mode of this tool only prints - a human pastes, edits and ratifies every
// block (see this package's own doc comment) - emit mode writes the two
// tables outright, alongside markerless.go's derived veto roster and
// internal/live/lint's logical-type classification. [identity.DefaultTable]
// and internal/live/lint's admittedTypesV0 are each declared, in full, by a
// file this mode owns end to end. Nothing hand-written participates in building either one: no per-cohort
// fragment, no init(), no core literal a batch appends to, and no assembly
// statement in a hand-edited file that unions generated pieces together. That
// last one matters as much as the rest - a human-maintained line that says
// "and here is how the generated parts become the table" is the same paste
// cycle in a smaller font.
//
// tools/estate-gen/overrides.go (Apply closures) and the stamp tables are
// explicitly out of scope; see main.go's flag help text.
//
// The bar is byte-identity: the file this mode writes must reconstruct the
// exact map [identity.DefaultTable] already holds - not an approximation, not
// a near-miss with a differently-worded Reason string. That rules out
// re-deriving a row's fields from row-gen's own fresh classification:
// renderServerAssignedEntry's Reason text is deliberately TEMPLATED
// boilerplate a human is meant to rewrite during ratification (reportHeader's
// own non-goals), and reconstructing it mechanically would silently diverge
// from whatever prose actually got ratified. So every field of every emitted
// row is copied verbatim from the ratified corpus.
//
// # Where that corpus lives, which is issue #263
//
// It used to be [identity.DefaultTable] - this mode's own output from the
// previous run. That made -emit a fixed point, in the sense that running it
// twice changed nothing. It did NOT make the artifact implied by the inputs,
// and the two were read as one thing. Measured at 5502e8a3de: empty
// DefaultTable's literal, run -emit twice, and the result is a 14-row table
// - exactly recordBackedRows' derived set - byte-identical across both runs,
// exit 0, converged, and 878 AWS rows gone. Every fixed-point and
// convergence check in this repository was satisfied there, because the
// classifier contributes no row and the corpus being copied from was the
// file being overwritten. Only `git checkout --` restored it.
//
// tools/row-gen/ratified.json is the cure: the same 878 rows in a file no
// generator writes, proven to render the committed table byte for byte
// (ratified.go, TestRatifiedRendersTheCommittedIdentityTable). runEmit loads
// it and hands it down, and the three reads that used to reach for the
// committed table have all moved onto it - [emittedRows] for the rows
// themselves, [markerlessRoster] for the server-assignment verdict that
// decides its veto, and [buildConvergence] for the population whose
// unreproduced half the #132 gate below refuses. Deleting a row from
// table_generated.go and re-emitting now restores it, because the row was
// never this generator's to lose.
//
// Two reads deliberately did NOT move, and both are comparisons against what
// currently ships rather than statements about what is ratified:
// runEmit's own retraction gate (retraction.go), which has to know what the
// committed table holds to notice a row leaving it, and recordBackedRows'
// dropped-row check below, for the same reason over the derived half.
//
// Three fields are the deliberate exceptions, all recomputed by
// renderIdentityFile on every run rather than copied, because nothing ever
// ratifies any of them by hand - the provider's own documentation or schema
// states them. mergeServerAssigned recomputes
// [identity.Component.ServerAssignedIfAbsent] from live/import-grammar.json,
// mergeCloudDefault recomputes [identity.Component.Attrs] on a cloud-bearing
// component from the same file's CloudDefault evidence, and
// mergeIdentityAttrs recomputes a server-assigned row's
// [identity.TypeIdentity.IdentityAttrs] from live/survey-full.json's
// identity schema (see serverassignedattrs.go). See renderIdentityFile's doc
// comment for why all three are still the fixed point this comment describes
// (idempotent once the field is set, changing only when the evidence does),
// not a hole in the byte-identity bar above.
//
// One whole class of row is the fourth, and it is derived rather than copied
// for the same reason: the RecordBacked rows, the record-store effects set
// issue #73 keeps outside the cloud entirely. Nothing about such a row is
// ratified - it carries no components, no reason string and no identity
// attributes, only its own type name and the flag - and the flag itself is a
// fact the provider's schema states. recordBackedRows derives the whole set
// from live/logical-schemas.json (see logicalschemas.go). Until that
// derivation existed the ten rows sat in annotations.json as unreproduced
// entries, every one carrying the same recorded exit, which was to do this.
//
// logicalClassRows reads the same artifact by the same rule to derive
// internal/live/lint's logicalTypes, the classification lint refuses or
// admits a store-only type on. Both sides of that pair have to move together
// or a type resolves under identity and is refused by lint before resolution
// runs, which is what happened when only the RecordBacked half was derived:
// four random_* types the hand-written lint table predated.
//
// What row-gen's fresh classifyAll run contributes is the MEASUREMENT: which
// types its registry-evidence classifier reproduces on its own and which
// still need a human's correction. That number is the visible debt a later
// effort shrinks by improving classify.go's rules, and it is reported to
// live/rowgen-convergence.json rather than by splitting the table into two
// Go variables - the table's shape should not encode how well the classifier
// happens to be doing this week.
const emitGeneratedByComment = "// Code generated by tools/row-gen -emit. DO NOT EDIT."

const (
	identityTableRel = "internal/live/identity/table_generated.go"
	lintTableRel     = "internal/live/lint/admission_generated.go"
	logicalTableRel  = "internal/live/lint/logical_type_generated.go"
)

// emitPartition is the convergence measurement: which admitted types a fresh
// row-gen classification reproduces (Generated) and which it does not
// (Override). Both halves are emitted into the same table - this is a count,
// not a partition of the source.
type emitPartition struct {
	Generated []string // TF types, sorted
	Override  []string // TF types, sorted
}

// runEmit is -emit's entry point: builds the same classified mapped set and
// convergence comparison -convergence measures, then writes the two files
// that declare [identity.DefaultTable] and admittedTypesV0. See this file's
// own doc comment for why the values come from tools/row-gen/ratified.json
// rather than from the fresh proposal.
func runEmit(out, errOut *os.File, allowRetraction bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	ratified, err := loadRatified(filepath.Join(root, ratifiedJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", ratifiedJSONRel, err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		return err
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", annotationsJSONRel, err)
	}
	grammar, err := loadImportGrammar(filepath.Join(root, importGrammarJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", importGrammarJSONRel, err)
	}
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", surveyJSONRel, err)
	}
	logical, err := loadLogicalSchemas(filepath.Join(root, logicalSchemasJSONRel))
	if err != nil {
		return fmt.Errorf("reading %s: %w", logicalSchemasJSONRel, err)
	}

	files, identityPart, lintPart, err := buildEmitFiles(ratified, proposals, annotations, grammar, survey, logical)
	if err != nil {
		return err
	}

	// retraction.go's gate, ahead of every write: a type that stops being
	// admitted stops resolving for every user who names it, so the drop has
	// to be deliberate. identity.AdmittedTypes() stays the baseline here on
	// purpose - the gate's question is what leaves the SHIPPED table, and only
	// the shipped table knows what ships today. emitPartition's two halves are
	// exactly emittedRows' key set, split by the convergence verdict.
	emitted := append(append([]string(nil), identityPart.Generated...), identityPart.Override...)
	if retracted := retractedTypes(identity.AdmittedTypes(), emitted); len(retracted) > 0 {
		if !allowRetraction {
			return retractionRefusal(retracted, allowRetractionFlag)
		}
		fmt.Fprintf(errOut, "row-gen -emit: %s: retracting %d admitted type(s):\n  %s\n",
			allowRetractionFlag, len(retracted), strings.Join(retracted, "\n  "))
	}

	for _, rel := range emitFileOrder {
		if err := os.WriteFile(filepath.Join(root, rel), files[rel], 0o644); err != nil { //nolint:gosec // fixed paths inside the checkout
			return fmt.Errorf("writing %s: %w", rel, err)
		}
		fmt.Fprintf(out, "wrote %s\n", rel)
	}
	if err := writeBucketsArtifact(root, proposals); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\n", bucketsJSONRel)

	fmt.Fprintf(errOut, "row-gen -emit: identity: %d types (%d reproduced by classifier, %d still corrected); lint: %d types (%d reproduced, %d corrected)\n",
		len(identityPart.Generated)+len(identityPart.Override), len(identityPart.Generated), len(identityPart.Override),
		len(lintPart.Generated)+len(lintPart.Override), len(lintPart.Generated), len(lintPart.Override))
	return nil
}

// emitFileOrder is the generated files' write order, and the key set
// buildEmitFiles' returned map always has exactly.
var emitFileOrder = []string{identityTableRel, lintTableRel, logicalTableRel, markerlessTableRel}

// buildEmitFiles is -emit's pure computation, split out from runEmit so tests
// can exercise it without writing to the checkout: given a fresh classifyAll
// run, annotations.json's rulings and live/import-grammar.json's own
// evidence, it returns the two generated files' contents by repo-relative
// path, plus the two convergence measurements the summary line and tests
// both want the counts of.
// ratified is the ratified corpus - tools/row-gen/ratified.json, an input no
// generator writes. Everything below derives from it rather than from
// [identity.DefaultTable]; see this file's own doc comment and issue #263.
func buildEmitFiles(ratified map[string]identity.TypeIdentity, proposals []proposal, annotations map[string]annotation, grammar map[string]importGrammarRow, survey map[string]surveyEntry, logical logicalSchemas) (files map[string][]byte, identityPart, lintPart emitPartition, err error) {
	recordBacked, err := recordBackedRows(ratified, logical)
	if err != nil {
		return nil, emitPartition{}, emitPartition{}, err
	}
	vetoed := markerlessRoster(ratified, survey, proposals, grammar)
	rows, types := emittedRows(ratified, recordBacked, grammar, survey, setOf(vetoed))

	// The convergence comparison runs over the rows about to be written, not
	// over the ones last written: a row that is ratified but not yet in the
	// committed table - the shape adding a row now takes - has to be compared
	// or the #132 gate below demands an annotation for it on no evidence.
	art := buildConvergence(rows, proposals, annotations)

	matched := make(map[string]bool, len(art.Types))
	for _, row := range art.Types {
		matched[row.TFType] = row.Matched
	}

	// Issue #132's gate: a row the fresh classifier does not reproduce is
	// only emittable when annotations.json records why - otherwise a row
	// diverging from the classifier is indistinguishable from a row nobody
	// has looked at. Types with no proposal at all (not_in_mapped_set)
	// are held to the same bar: no evidence path reaching a type is itself
	// a fact that needs a ruling. -convergence stays ungated on purpose:
	// it is the measurement, and it must keep running over an unannotated
	// table so the debt stays visible.
	//
	// A RecordBacked row is exempt, because it is no longer an unreproduced
	// row: recordBackedRows derives it from the provider's own schema (see
	// logicalschemas.go), the same standing mergeServerAssigned's and
	// mergeIdentityAttrs' fields have. That exemption is what retires the ten
	// rulings annotations.json used to carry for exactly these types, each of
	// which named this derivation as its own exit.
	var unruled []string
	for _, t := range types {
		if matched[t] || recordBacked[t] {
			continue
		}
		if _, ok := annotations[t]; !ok {
			unruled = append(unruled, t)
		}
	}
	if len(unruled) > 0 {
		return nil, emitPartition{}, emitPartition{}, fmt.Errorf(
			"row-gen -emit: %d admitted type(s) are neither reproduced by the fresh classifier nor ruled in %s - every unreproduced row needs a recorded reason before it can ship:\n  %s",
			len(unruled), annotationsJSONRel, strings.Join(unruled, "\n  "))
	}

	identityPart = partitionAdmitted(types, matched, recordBacked)
	lintGenerated, lintOverride := splitNonRecordBacked(identityPart, recordBacked)
	lintPart = emitPartition{Generated: lintGenerated, Override: lintOverride}

	identitySrc, err := renderIdentityFile(types, rows)
	if err != nil {
		return nil, emitPartition{}, emitPartition{}, fmt.Errorf("rendering %s: %w", identityTableRel, err)
	}
	lintSrc, err := renderLintFile(nonRecordBacked(types, recordBacked))
	if err != nil {
		return nil, emitPartition{}, emitPartition{}, fmt.Errorf("rendering %s: %w", lintTableRel, err)
	}
	logicalRows, err := logicalClassRows(logical)
	if err != nil {
		return nil, emitPartition{}, emitPartition{}, err
	}
	logicalSrc, err := renderLogicalTypeFile(logicalRows, logicalFamilyPrefixesOf(logical))
	if err != nil {
		return nil, emitPartition{}, emitPartition{}, fmt.Errorf("rendering %s: %w", logicalTableRel, err)
	}
	markerlessSrc, err := renderMarkerlessFile(vetoed)
	if err != nil {
		return nil, emitPartition{}, emitPartition{}, fmt.Errorf("rendering %s: %w", markerlessTableRel, err)
	}

	return map[string][]byte{
		identityTableRel:   identitySrc,
		lintTableRel:       lintSrc,
		logicalTableRel:    logicalSrc,
		markerlessTableRel: markerlessSrc,
	}, identityPart, lintPart, nil
}

// setOf indexes a sorted type list for membership tests.
func setOf(types []string) map[string]bool {
	out := make(map[string]bool, len(types))
	for _, t := range types {
		out[t] = true
	}
	return out
}

// recordBackedRows is the RecordBacked half of the emitted table, derived
// from live/logical-schemas.json rather than copied from the ratified corpus
// (see [recordBackedTypes] for the rule).
//
// It also refuses two shapes outright rather than letting them through as a
// silent table change, and the two read different sources on purpose.
//
// The first is a derived type the RATIFIED CORPUS also carries as an
// ordinary, non-record row: two evidence sources claiming one type, which no
// prefix in the input providers can produce today and which should stop the
// run if it ever can. That question is about what is ratified, so it asks
// ratified.json - which is also the only place a hand edit can now introduce
// the collision. (loadRatified already refuses a stored row that declares
// itself RecordBacked, so the corpus half of this pair is always the ordinary
// kind.)
//
// The second is a currently-RecordBacked row the derivation does not
// reproduce: a row DROPPED from the admission table by a generator run, a
// type that resolves today and would stop. That question is about what
// SHIPS, so it stays on [identity.DefaultTable] - and it has to, because
// ratified.json holds no record-backed row to compare against.
func recordBackedRows(ratified map[string]identity.TypeIdentity, logical logicalSchemas) (map[string]bool, error) {
	derived := recordBackedTypes(logical)
	backed := make(map[string]bool, len(derived))
	for _, t := range derived {
		if _, ok := ratified[t]; ok {
			return nil, fmt.Errorf(
				"row-gen -emit: %s derives RecordBacked from %s, but %s already ratifies it as an ordinary row - two evidence sources are claiming one type",
				t, logicalSchemasJSONRel, ratifiedJSONRel)
		}
		backed[t] = true
	}
	var dropped []string
	for _, t := range identity.AdmittedTypes() {
		if identity.DefaultTable[t].RecordBacked && !backed[t] {
			dropped = append(dropped, t)
		}
	}
	if len(dropped) > 0 {
		return nil, fmt.Errorf(
			"row-gen -emit: %d RecordBacked row(s) in %s are not reproduced by the derivation over %s, so emitting would remove them from the admission table - re-run -logical-schemas, or fix the rule:\n  %s",
			len(dropped), identityTableRel, logicalSchemasJSONRel, strings.Join(dropped, "\n  "))
	}
	return backed, nil
}

// emittedRows builds every row the identity table will carry, and the sorted
// type list that keys it.
//
// markerless.go's derived veto FILTERS here (issue #249): a type the rule
// vetoes gets no row, whether or not a ratification batch had already
// written one. The veto is a rule about what the mechanism can do, so it
// has to reach backwards as well as forwards - a row admitted before the
// rule existed is not evidence against it, and leaving those rows in place
// meant the table promised support for types whose every instance would
// reach internal/live/stamp's unmarked-apply refusal at apply time.
//
// Retraction alone would have been a measurement change dressed as a
// support change: lint's plain unadmitted-type finding is NON-blocking in
// internal/live/check's ClassifyOnboarding, so estates would have climbed
// the onboarding ladder while nothing became applyable. That is why
// lint.RuleMarkerlessType had to land first. It is blocking, it fires
// ahead of RuleUnadmittedType, and it sits between this table and
// [identity.SynthesizeTypeIdentity] so a retracted row cannot fall through
// to schema-fallback admission instead. With that in place the ladder moves
// the honest way: a configuration naming a vetoed type reports itself
// blocked, which is what it is.
//
// internal/live/harness's "markerless-veto-admitted-overlap" entry is the
// ratchet, now at zero, and it fails upwards: a hand-pasted row for a
// vetoed type would be caught there as well as silently dropped here.
//
// The two halves come from different places on purpose. A RecordBacked row is
// derived whole - its only non-zero fields are Type and RecordBacked, because
// a record-backed type has no components to resolve and nothing about it is
// ratified by hand. Every other row is copied verbatim from the ratified
// corpus, with mergeServerAssigned's, mergeCloudDefault's and
// mergeIdentityAttrs' three recomputed fields layered over it.
//
// ratified is that corpus, and it is tools/row-gen/ratified.json - an input
// no generator writes. It used to be [identity.DefaultTable], this
// generator's own previous output, which is the whole of issue #263. See
// ratified.go.
func emittedRows(ratified map[string]identity.TypeIdentity, recordBacked map[string]bool, grammar map[string]importGrammarRow, survey map[string]surveyEntry, vetoed map[string]bool) (map[string]identity.TypeIdentity, []string) {
	rows := make(map[string]identity.TypeIdentity, len(ratified)+len(recordBacked))
	for _, t := range sortedRatifiedKeys(ratified) {
		if ratified[t].RecordBacked {
			continue // re-derived below, never copied
		}
		if vetoed[t] {
			continue // markerless: no marker can attach, so no row
		}
		rows[t] = mergeIdentityAttrs(mergeCloudDefault(mergeServerAssigned(ratified[t], grammar[t]), grammar[t]), survey[t])
	}
	for t := range recordBacked {
		rows[t] = identity.TypeIdentity{Type: t, RecordBacked: true}
	}
	types := make([]string, 0, len(rows))
	for t := range rows {
		types = append(types, t)
	}
	sort.Strings(types)
	return rows, types
}

// partitionAdmitted splits the emitted type set by matched, the fresh
// convergence comparison's verdict. A type absent from matched entirely
// (rowgen-convergence's "not in the mapped set": no fresh proposal exists to
// compare) counts as unreproduced, the same as a type whose proposal actively
// disagrees - except for a RecordBacked type, which is reproduced by a rule
// of its own and so counts as generated.
func partitionAdmitted(types []string, matched, recordBacked map[string]bool) emitPartition {
	var part emitPartition
	for _, t := range types {
		if matched[t] || recordBacked[t] {
			part.Generated = append(part.Generated, t)
		} else {
			part.Override = append(part.Override, t)
		}
	}
	return part
}

// splitNonRecordBacked derives admittedTypesV0's own measurement from the
// identity table's: a RECORD_ADMITTED type is never a member of
// admittedTypesV0 at all - internal/live/lint refuses it before resolution
// ever runs - so every such type is dropped here regardless of which half it
// landed in.
func splitNonRecordBacked(part emitPartition, recordBacked map[string]bool) (generated, override []string) {
	for _, t := range part.Generated {
		if !recordBacked[t] {
			generated = append(generated, t)
		}
	}
	for _, t := range part.Override {
		if !recordBacked[t] {
			override = append(override, t)
		}
	}
	return generated, override
}

// nonRecordBacked is admittedTypesV0's key set: the emitted table's own keys
// minus the RecordBacked rows. This is the whole of admittedTypesV0's
// relationship to the identity table, which is why it is derived here rather
// than tracked as a list of its own.
func nonRecordBacked(types []string, recordBacked map[string]bool) []string {
	var out []string
	for _, t := range types {
		if !recordBacked[t] {
			out = append(out, t)
		}
	}
	return out
}

// renderIdentityFile renders internal/live/identity's whole table: the
// [identity.DefaultTable] declaration itself, over the named types' entries,
// gofmt-formatted. The result is self-contained - a plain map literal that
// calls no constructor and references no hand-written helper or constant -
// so that nothing outside this generator participates in building the
// table.
//
// The rows come from emittedRows: a RecordBacked row is derived whole from
// live/logical-schemas.json, and every other row is a verbatim copy of the
// ratified entry with three recomputed fields layered over it.
//
// Every field but two is copied verbatim from tools/row-gen/ratified.json,
// as this file's own doc comment has always promised of whatever corpus was
// supplying the rows. The exceptions are mergeServerAssigned's own field,
// [identity.Component.ServerAssignedIfAbsent] (#190), mergeCloudDefault's
// [identity.Component.Attrs] on a component that also names a cloud property
// (#241), and mergeIdentityAttrs'
// [identity.TypeIdentity.IdentityAttrs] on a server-assigned row (#197):
// unlike every other field here, nothing ever ratifies these values by hand -
// a human choosing one per row would be re-deciding a fact the provider's
// docs or its own identity schema already state, the same reasoning that
// keeps OmittedFallbacks's Default values (and, before this, ForceNew and
// Required) out of hand-ratification too. So none of them is read from
// DefaultTable at all; they are recomputed from live/import-grammar.json and
// live/survey-full.json on every run, over an otherwise-verbatim copy of
// every ratified row. A row a human ratified before either rule existed
// still gets its field filled in the moment the evidence for its own
// arguments says to, with no per-type edit anywhere in this generator's
// control flow.
func renderIdentityFile(types []string, rows map[string]identity.TypeIdentity) ([]byte, error) {
	var b strings.Builder
	b.WriteString(licenseHeader)
	b.WriteString("\n")
	b.WriteString(emitGeneratedByComment)
	b.WriteString("\n\n")
	b.WriteString("package identity\n\n")
	b.WriteString(defaultTableDoc)
	b.WriteString("var DefaultTable = map[string]TypeIdentity{\n")
	for _, t := range types {
		fmt.Fprintf(&b, "%q: %s,\n", t, renderStruct(reflect.ValueOf(rows[t])))
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}

// mergeServerAssigned layers live/import-grammar.json's Argument Reference
// evidence onto entry's Components, setting
// [identity.Component.ServerAssignedIfAbsent] wherever the doc's own bullet
// for one of a component's Attrs states the auto-generation convention
// (tools/importdocs-gen's ArgumentRefEntry.ServerAssignedIfAbsent - see
// #190). It never touches a component that already resolves without the
// argument (Default set) or that carries no Attrs at all (a literal
// separator), and it never names a resource type: the only inputs are
// entry's own Attrs names and row's own per-argument evidence, so the same
// call handles every type in the table identically. row's zero value (the
// type has no grammar row at all) leaves entry untouched.
func mergeServerAssigned(entry identity.TypeIdentity, row importGrammarRow) identity.TypeIdentity {
	if len(row.ArgumentReference) == 0 || len(entry.Components) == 0 {
		return entry
	}
	assigned := make(map[string]bool, len(row.ArgumentReference))
	for _, a := range row.ArgumentReference {
		if a.ServerAssignedIfAbsent {
			assigned[a.Name] = true
		}
	}
	if len(assigned) == 0 {
		return entry
	}
	changed := false
	comps := append([]identity.Component(nil), entry.Components...)
	for i, c := range comps {
		if c.ServerAssignedIfAbsent || c.Default != "" || len(c.Attrs) == 0 {
			continue
		}
		for _, name := range c.Attrs {
			if assigned[name] {
				c.ServerAssignedIfAbsent = true
				comps[i] = c
				changed = true
				break
			}
		}
	}
	if !changed {
		return entry
	}
	entry.Components = comps
	return entry
}

// mergeCloudDefault is mergeServerAssigned's sibling for the other direction
// of the same evidence: where that one records that omitting an argument
// means "the provider will make one up", this one records that omitting it
// means "the cloud property this component already names"
// (tools/importdocs-gen's ArgumentRefEntry.CloudDefault - "If omitted, this
// defaults to the AWS Account ID", "Defaults to the Region set in the
// provider configuration"). It fills in [identity.Component.Attrs] on a
// component that already carries a matching [identity.Component.Cloud] and
// no Attrs of its own, which is what lets the resolver prefer the value the
// configuration stated over the one it was never told - see
// [identity.Component.Cloud]'s doc comment and the resolver's
// cloudComponentAttr.
//
// Like mergeServerAssigned it names no resource type: the only inputs are
// the component's own Cloud value and row's own per-argument evidence, and
// the match is that the two agree on which cloud property is at stake. A
// component whose Cloud is the account is never given a region-defaulting
// argument, and neither is ever given an argument the docs say nothing about.
// A component that already carries Attrs is left alone, so a row a human
// ratified with both halves stays exactly as ratified.
func mergeCloudDefault(entry identity.TypeIdentity, row importGrammarRow) identity.TypeIdentity {
	if len(row.ArgumentReference) == 0 || len(entry.Components) == 0 {
		return entry
	}
	// First bullet wins per cloud property, matching the doc order the
	// scrape preserves; in v6.59.0 no type documents two arguments as
	// defaulting to the same property anyway.
	byCloud := make(map[string]string, 2)
	for _, a := range row.ArgumentReference {
		if a.CloudDefault == "" {
			continue
		}
		if _, seen := byCloud[a.CloudDefault]; !seen {
			byCloud[a.CloudDefault] = a.Name
		}
	}
	if len(byCloud) == 0 {
		return entry
	}
	changed := false
	comps := append([]identity.Component(nil), entry.Components...)
	for i, c := range comps {
		if c.Cloud == identity.CloudNone || len(c.Attrs) > 0 {
			continue
		}
		name, ok := byCloud[string(c.Cloud)]
		if !ok {
			continue
		}
		c.Attrs = []string{name}
		comps[i] = c
		changed = true
	}
	if !changed {
		return entry
	}
	entry.Components = comps
	return entry
}

// defaultTableDoc is DefaultTable's own doc comment. It travels with the
// generator because the declaration does: the table's meaning is a fact about
// this generator's output, not a note a maintainer keeps beside it.
const defaultTableDoc = `// DefaultTable is the v0 identity table: every AWS resource type the
// stateless subset admits, keyed by provider-local type name. A type absent
// from this table is outside the subset and resolving it is an error.
//
// Every row is derived by tools/row-gen from the provider's own schema, the
// CloudFormation Registry's primaryIdentifier, and the provider documentation's
// import grammar. Corrections belong in the generator's rules or in its
// annotation ledger (tools/row-gen/annotations.json), never here: this file is
// overwritten in full on every run.
`

// renderLintFile renders internal/live/lint's admittedTypesV0 declaration.
func renderLintFile(types []string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(licenseHeader)
	b.WriteString("\n")
	b.WriteString(emitGeneratedByComment)
	b.WriteString("\n\n")
	b.WriteString("package lint\n\n")
	b.WriteString(admittedTypesDoc)
	b.WriteString("var admittedTypesV0 = map[string]struct{}{\n")
	for _, t := range types {
		fmt.Fprintf(&b, "%q: {},\n", t)
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}

// admittedTypesDoc is admittedTypesV0's own doc comment, carried by the
// generator for the same reason defaultTableDoc is.
const admittedTypesDoc = `// admittedTypesV0 is every provider-local resource type a stateless
// configuration may name. It is exactly internal/live/identity's DefaultTable
// key set minus the RECORD_ADMITTED types, which lint refuses before identity
// resolution ever runs.
//
// Keyed by provider-local type name (the first label of a resource block), not
// by fully-qualified provider address, because that is what a configuration
// author writes and what the error message has to name back to them.
`

// renderLogicalTypeFile renders internal/live/lint's logicalFamilyPrefixes
// and logicalTypes from live/logical-schemas.json.
//
// Both used to be hand-written, and the table's own doc comment described the
// rows as "hand-verified ... for every type this file's audit could
// confidently place". That audit ran once, against hashicorp/random 3.8, and
// could not place a type released after it: random_uuid4 and random_uuid7
// shipped in 3.9.0 and got the no-row-found default, while random_string and
// random_uuid had been left unplaced from the start. Meanwhile
// recordBackedRows derived all four, so identity admitted them and lint
// refused them - the layer split TestNoLogicalTypeIsAdmittedWithoutAnIdentityRow
// exists to catch, running in the direction that test could not see, because
// it iterates lint's rows and these types had none.
//
// Rendering both from the artifact is what closes it for the next release
// too: -logical-schemas re-measures, -emit re-derives, and neither mode names
// a resource type.
func renderLogicalTypeFile(rows []logicalClassRow, prefixes []string) ([]byte, error) {
	var b strings.Builder
	b.WriteString(licenseHeader)
	b.WriteString("\n")
	b.WriteString(emitGeneratedByComment)
	b.WriteString("\n\n")
	b.WriteString("package lint\n\n")
	b.WriteString(logicalFamilyPrefixesDoc)
	b.WriteString("var logicalFamilyPrefixes = []string{\n")
	for _, p := range prefixes {
		fmt.Fprintf(&b, "%q,\n", p)
	}
	b.WriteString("}\n\n")
	b.WriteString(logicalTypesDoc)
	b.WriteString("var logicalTypes = map[string]LogicalType{\n")
	for _, r := range rows {
		fmt.Fprintf(&b, "%q: {Type: %q, Class: %s, Prefix: %q, Evidence: %q},\n",
			r.Type, r.Type, r.Class, r.Prefix, r.Evidence)
	}
	b.WriteString("}\n")
	return format.Source([]byte(b.String()))
}

// logicalFamilyPrefixesDoc and logicalTypesDoc are the two generated
// variables' doc comments, carried by the generator the same way
// admittedTypesDoc is.
const logicalFamilyPrefixesDoc = `// logicalFamilyPrefixes are the provider-local type prefixes whose resources
// exist only inside OpenTofu's own record of them. See live/LIMITATIONS.md.
//
// Membership here is what makes a type with no [logicalTypes] row of its own
// still classify - as [ClassOtherRefused], except the tls_ family, see
// [ClassifyLogicalType] - instead of falling through to the generic
// unadmitted-type refusal. A random_id gets the logical-resource explanation
// for why its whole family is out, not the generic "not in the v0 table"
// message.
//
// One entry per provider in tools/row-gen's logicalProviderSources, taken
// from the last segment of its source address. hashicorp/local is here and
// contributes no [logicalTypes] row: its resources are not store-only, so its
// types keep the no-row-found default. OpenTofu's built-in provider
// contributes no prefix at all - terraform_data is admitted by exact type
// name, because a "terraform_" prefix would claim a whole family this fork
// has never surveyed.
`

const logicalTypesDoc = `// logicalTypes is the per-type classification table, derived from
// live/logical-schemas.json: every managed resource type served by a
// store-only provider, classified by the rule that a live (non-deprecated)
// sensitive attribute anywhere in the type's schema means it handles material
// live/RECEIPTS.md's no-secrets rule forbids a record from carrying, and none
// means the record can hold the type's whole value.
//
// It is the same rule, over the same artifact, that derives
// [identity.TypeIdentity.RecordBacked] - deliberately, so that lint's
// RECORD_ADMITTED and identity's RecordBacked cannot name different sets.
// They did once: the four types the hand-written table predated resolved
// under identity and were refused outright by lint, so a configuration with a
// record_store declared got told its type was out of the subset.
//
// A type in a [logicalFamilyPrefixes] family with no row here classifies
// [ClassOtherRefused] by default (see [ClassifyLogicalType]).
`

// renderStruct renders one struct value as a Go composite literal, field by
// field, by reflection - never by a field list written out here. That is the
// point: [identity.TypeIdentity] gaining, losing or renaming a field changes
// this generator not at all, where a hand-listed renderer would have to be
// edited in lockstep and would silently drop the new field until someone
// noticed. Zero-valued fields are omitted, which is exactly as faithful as
// spelling them out and considerably shorter; a nil slice and an empty one
// are distinguished, because [identity.TypeIdentity.IdentityAttrs] gives them
// different meanings (see its doc comment).
func renderStruct(v reflect.Value) string {
	t := v.Type()
	var parts []string
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if f.PkgPath != "" {
			continue // unexported
		}
		fv := v.Field(i)
		if isZero(fv) {
			continue
		}
		parts = append(parts, fmt.Sprintf("%s: %s", f.Name, renderValue(fv)))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

// renderValue renders one value as a Go literal. The element type of a slice
// is elided ([]Component{{...}} rather than []Component{Component{...}}),
// matching this repository's own gofmt -s style rather than the fully-spelled
// form most Go source generators with no simplify pass produce.
func renderValue(v reflect.Value) string {
	switch v.Kind() {
	case reflect.String:
		return fmt.Sprintf("%q", v.String())
	case reflect.Bool:
		return fmt.Sprintf("%t", v.Bool())
	case reflect.Slice:
		var elems []string
		for i := 0; i < v.Len(); i++ {
			e := v.Index(i)
			if e.Kind() == reflect.Struct {
				elems = append(elems, renderStruct(e))
			} else {
				elems = append(elems, renderValue(e))
			}
		}
		return fmt.Sprintf("[]%s{%s}", v.Type().Elem().Name(), strings.Join(elems, ", "))
	case reflect.Struct:
		return renderStruct(v)
	default:
		panic(fmt.Sprintf("row-gen -emit: no rendering for %s (kind %s) - teach renderValue about it", v.Type(), v.Kind()))
	}
}

// isZero reports whether a field should be omitted from the rendered literal.
// It is reflect.Value.IsZero for everything except slices, where a nil slice
// is omitted but an empty non-nil one is not: the two mean different things
// in [identity.TypeIdentity], and collapsing them would not round-trip.
func isZero(v reflect.Value) bool {
	if v.Kind() == reflect.Slice {
		return v.IsNil()
	}
	return v.IsZero()
}

// licenseHeader matches every other file in this repository.
const licenseHeader = `// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0
`
