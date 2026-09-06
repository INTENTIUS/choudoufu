// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The two ratchet constants this file used to carry are entries in
// internal/live/harness now; the provenance below is kept because it is the
// evidence behind their bounds. Issue #695 renamed the artifact those bounds
// are measured on from live/rowgen-convergence.json to
// live/rowgen-mismatches.json and dropped the adopted-unchanged ratio the old
// one led with, so a percentage quoted in the history below is a number that
// was measured once and is no longer computed anywhere.

// unannotatedMismatchRatchetMax was this measurement's own downward
// ratchet, the "delete-me ratchet idiom" tools/mapping-gen's own
// unclassified-count ratchet already used for live/mapping.json: the
// highest live/rowgen-mismatches.json's summary.unannotated_mismatches may
// be. Both are now entries in internal/live/harness; what follows is the
// provenance for this one's bound, kept because it is the evidence. This pass originally measured 170
// genuine mismatches over 571 admitted types, after merging
// ratify-sagemaker/ratify-governance/ratify-media. It was then bumped to
// 207 (125 scrape-gap) after a further eight concurrently-landed batches
// (ratify-data-movement, ratify-networking-advanced, ratify-databases,
// ratify-security, ratify-ec2-networking, ratify-iot,
// ratify-ai-location/stragglers/connect-euc among them) raised the admitted
// count to 652, none of them ever having passed through this
// check before (it did not exist yet when they were ratified).
//
// Merging ratify-remainder (issue #65's REMAINDER batch, the long tail of
// services outside every concurrent batch's own scope) raised the admitted
// count again, 652 to 836 (184 more - the exact count
// live/e2e/estates/remainder/README.md's own "184 types admitted" gives),
// and, the same as every prior batch above, never having passed through
// this check before, surfaces its own row-gen/DefaultTable
// disagreements here for the first time too: 34 more unannotated mismatches
// (241 total: 158 scrape-gap, up from 125; 83 non-scrape-gap, up from 82).
// This lines up with REMAINDER's own README, whose "Corrections made (35
// types...)" section is the same shape of deliberate row-gen disagreement
// this ratchet measures.
//
// Merging importdocs-widen then moved it back down, which is the direction
// this ratchet is built to travel. That branch is the "wider
// importdocs-gen scrape" the paragraph below already anticipated: its
// widened parse plus the new row-gen import-precedence rules (rules 1-9,
// tools/row-gen/importprecedence.go) landed deliberately *after*
// ratify-remainder, so one final regeneration applied them across the
// whole admitted set rather than only the part that predated the batch.
// Measured over all 846 admitted types (836 hand-written entries in
// internal/live/identity/table.go plus the 10 record-backed types in
// table_recordbacked.go), that closed 26 of REMAINDER's mismatches
// outright - 241 down to 215, of which 117 are scrape-gap, down from 158 -
// and lifted adopted-unchanged from 70.79% to 73.94%. So 215 is a measured
// floor read straight off live/rowgen-mismatches.json's own committed
// summary, not a bump: the three batches that contributed to it are
// ratify-remainder (+184 admitted types), the eight concurrently-landed
// batches named above, and importdocs-widen (-26 mismatches).
//
// See tools/row-gen/annotations.json's own doc comment for why none of the
// 215 is annotated yet. Lower this constant to match
// live/rowgen-mismatches.json's own committed count whenever a future
// change (a wider importdocs-gen scrape, a fold-child Components rule,
// annotations.json gaining real rulings) closes some of the gap. Raising
// it back up again needs its own reviewed reason, not a silent increase -
// TestUnannotatedMismatchRatchet below fails the build the moment a
// regeneration would do that.
//
// importprecedence.go's applyImportGrammarPrecedence used to skip every
// fold row before any of its rules ran (a guard reading
// "if p.CFNType == \"\" { continue }", justified by a comment claiming
// every rule below gates on PrimaryIdentifier - false for tryGrammarComposite
// and tryArgumentReferenceValueMatch, which read only the import-grammar row
// and p.Bucket). Deleting that guard is exactly the "fold-child Components
// rule" this comment already anticipated above: of the 49 fold-child rows
// that were unconditionally mismatched (bucketFoldChild never claims
// Components, so it could never match a ratified composite/client-named
// entry), 21 now resolve through those same two rules, with zero rows that
// previously matched now mismatching (verified by a full before/after diff
// over the compared set, not just the fold-child rows). 215 down to 194.
//
// Issue #132's derivation phase then took 194 to 114 through seven
// extractor commits (89bae9fb27..c9632d8b23), and the gate-and-annotations
// step closed the rest: every remaining mismatch carries a ruling in
// tools/row-gen/annotations.json, -emit refuses an unruled one, and this
// constant reaches its floor. It stays 0 from here - a regeneration that
// produces a new unannotated mismatch is either a real regression or a new
// admission that has not been ruled, and both should fail the build. The
// count that still travels downward is the ledger's own size,
// annotationCountRatchetMax below.
// The bound moved into the burndown registry in internal/live/harness on
// 2026-08-16, as the "rowgen-unannotated-mismatches" entry. The measurement
// moved with it and got stricter: the entry recomputes both counts from the
// artifact's own rows, cross-checks them against summary.genuine_mismatches
// and summary.unannotated_mismatches, and reads tools/row-gen/annotations.json
// to confirm each unmatched row is genuinely ruled. The const trusted the
// summary field this file writes, which is a number checking itself.

// annotationCountRatchetMax is the other half of issue #132's ratchet pair:
// the number of rulings tools/row-gen/annotations.json may carry. The
// unannotated ratchet above going to 0 means every unreproduced row is
// ruled; without this second ceiling the ledger would only ever grow,
// because adding a ruling is always easier than fixing an extractor. The
// count only travels downward: an extractor fix that reproduces a ruled row
// makes its annotation stale (TestAnnotationsAgreeWithMismatches demands
// its deletion), and this constant is then lowered to match. Raising it
// needs its own reviewed reason - a newly admitted type the classifier
// cannot reproduce - not a silent increase.
//
// The committed 128 is 107 genuine mismatches (the ledger's own
// summary) plus the 21 not_in_mapped_set types, which never appear in the
// artifact's rows at all (no proposal exists to compare) but are held to
// the same bar by the -emit gate. Every ruling carries an exit naming what
// a fuller extraction would have to capture; the ledger is a list of named
// extractor gaps, not accepted losses.
//
// 129 (2026-08-15): the reviewed bump the constant's own rule allows - the
// #175 reversal batch admitted aws_cloudwatch_event_target, a fold-child
// the classifier deliberately does not shape (its ruling names the exit: a
// fold-child rule composing the parent's tuple with the child's own
// import-doc arguments).
//
// 122 (2026-08-15): the downward travel the constant's own rule describes,
// and the first time a whole exit condition was met at once. Eleven rulings
// shared the exit "an evidence path not rooted in the CFN registry:
// classifyAll would have to propose from the scraped doc grammar alone when
// live/mapping.json records cfn_type null". loadMapping stopped filtering
// the mapping artifact by via and classifyUnmapped built that path, so
// row-gen now proposes for all 439 types CloudFormation does not model; 7
// of the 11 became stale and were deleted. The remaining 4 are reached now
// but not reproduced, and their rulings were rewritten to say so rather
// than left asserting a "no proposal reaches this type" that stopped being
// true.
//
// 119 (2026-08-15): three more, from the same batch's second extractor fix.
// aws_s3_bucket_versioning, _lifecycle_configuration and
// _server_side_encryption_configuration were each ruled with the same
// sentence - "the doc documents two import forms (the bucket alone, or
// bucket and expected_bucket_owner comma-joined); the ratified row takes the
// single-argument form, which the scraped one-separator grammar row cannot
// express". tryDocumentedShorterForm (importprecedence.go rule 1b) makes it
// expressible, and the classifier now reproduces all three ratified rows
// without having been shown them.
//
// 120 (2026-08-15): #175's demand-head batch admitted aws_security_group_rule.
// mapping-gen assigns it no CFN model at all (both AWS::EC2::SecurityGroup
// {Ingress,Egress} are already claimed by the split per-direction successor
// types), and the documented import ID's fixed five-argument prefix is
// followed by a variable-count trailing source/destination group every
// composite rule's arity check declines rather than guess the shape of -
// there is no proposal to reproduce and no vocabulary yet for a
// one-or-more-of-a-named-set trailing component. See the ruling's own exit.
//
// 121 (2026-08-16): the reviewed bump the constant's own rule allows - a
// newly admitted type the classifier cannot reproduce. aws_alb_target_group_
// attachment (the rejected.json sweep: the aws_alb* family are the
// provider's own documented aliases of aws_lb*, "is known as ... The
// functionality is identical.", and importdocs-gen now clones the canonical
// type's import-grammar row onto the alias - see aliasDeclaredFor) inherits
// aws_lb_target_group_attachment's own fold-child gap verbatim, on cloned
// evidence, so it needs the same ruling under its own name.
//
// 116 (2026-08-16): the downward travel the constant's own rule describes.
// The same sweep taught classifyGrammar a plain-prose enumeration signal
// (plainEnumComposedArguments, prosename.go): an Import sentence naming
// every segment of a composite ID in plain words with no backticks at all
// ("using the listener arn and certificate arn, separated by an underscore"
// on aws_lb_listener_certificate's own page) now sets composed_of_arguments
// the same way the backticked and format-token signals already did. Five
// of the rows this newly resolves already carried a ruling under the old
// "needs-hand-separator"/"fold-child, no scrape attribution" reasoning -
// aws_cognito_identity_provider, aws_db_instance_role_association,
// aws_guardduty_filter, aws_guardduty_member, aws_rds_cluster_role_
// association - and the fresh classifier now reproduces all five ratified
// rows unchanged, so their rulings are deleted rather than left stale.
// Net against 121: -5 stale +1 new (aws_alb_target_group_attachment) = 116.
//
// 95 (2026-08-16): two drops in one, and the constant was already stale by
// the first. The committed ledger was at 105 when this was last read, so 116
// had stopped bounding anything - the note above accounts for the batch that
// set it and not for whatever removed nine more afterwards, which is exactly
// the failure a ratchet is supposed to make visible. The second drop is
// deliberate and is what this bump records: the ten record-backed effects
// types (null_resource, terraform_data, random_id/integer/pet/shuffle,
// time_offset/rotating/sleep/static) each carried a ruling whose own Exit
// field named the same fix - "Retire by deriving the RecordBacked rows ...
// inside -emit instead of carrying them as unreproduced table rows" - and
// -emit now does exactly that from live/logical-schemas.json
// (recordBackedRows, emit.go). A derived row is not an unreproduced one, so
// the rulings are deleted rather than left to quietly exempt something else.
// 105 - 10 = 95.
// The bound moved into the burndown registry in internal/live/harness on
// 2026-08-16, as the "rowgen-annotation-rulings" entry, and two things
// surfaced in the move. It gained a denominator - the ledger
// artifact's admitted_total - because the cheapest way to delete a ruling
// is to un-admit the type it names, which lowers the count while removing
// support, and nothing recorded that. And the const was measured stale for
// the second time in two days: the committed ledger carried 93 rulings
// against a ceiling of 95, so it had stopped bounding anything. The entry's
// bound is 93, taken from the measurement rather than from this comment.

// TestMismatchLedgerMatchesCommitted is the drift half the ratchet
// test above deliberately does not do itself (mapping_gen_test.go's own
// TestUnclassifiedCountRatchet keeps that same separation, for the same
// reason its own doc comment gives: the ratchet stays a working, cheap
// check on its own even if this drift test's shape changes later). This
// test regenerates the comparison from the checked-out sources - the same
// classifyAll pipeline -mismatches uses - and requires it to match the
// committed artifact byte-for-byte modulo JSON formatting, so a change to
// classify.go, importprecedence.go or annotations.json without a
// regeneration of live/rowgen-mismatches.json fails the build instead of
// silently drifting.
func TestMismatchLedgerMatchesCommitted(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	proposals, err := loadProposals(root)
	if err != nil {
		t.Fatalf("loadProposals: %v", err)
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		t.Fatalf("loadAnnotations: %v", err)
	}
	fresh := buildMismatchLedger(buildComparison(loadEmittedTableForTest(t, proposals), proposals, annotations))

	committed := loadCommittedMismatchLedger(t)

	freshJSON, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the fresh ledger: %v", err)
	}
	committedJSON, err := json.MarshalIndent(committed, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the committed ledger: %v", err)
	}
	if string(freshJSON) != string(committedJSON) {
		t.Errorf("%s has drifted from a fresh regeneration - regenerate with:\n  go run ./tools/row-gen -mismatches\n\nSummary drift: committed=%+v fresh=%+v",
			mismatchesJSONRel, committed.Summary, fresh.Summary)
	}
}

// TestSchemaPrecedenceArtifactMatchesCommitted is the same drift check for
// live/schema-precedence.json, the other half of what
// live/rowgen-convergence.json used to carry. It is a separate test because
// it is a separate measurement with a separate regeneration command, and
// because its inputs are different ones: tools/row-gen/ratified.json and
// live/import-grammar.json, neither of which the mismatch ledger reads
// directly.
func TestSchemaPrecedenceArtifactMatchesCommitted(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	fresh := schemaPrecedenceArtifact{
		GeneratedBy:            "",
		schemaReproducesBucket: buildSchemaReproducesBucket(loadRatifiedForTest(t), loadImportGrammarForTest(t)),
	}

	data, err := os.ReadFile(filepath.Join(root, schemaPrecedenceJSONRel))
	if err != nil {
		t.Fatalf("reading %s: %v (run: go run ./tools/row-gen -schema-precedence)", schemaPrecedenceJSONRel, err)
	}
	var committed schemaPrecedenceArtifact
	if err := json.Unmarshal(data, &committed); err != nil {
		t.Fatalf("decoding %s: %v", schemaPrecedenceJSONRel, err)
	}
	// The provenance fields are not part of the measurement; compare the
	// bucket alone so a reworded note is not read as drift.
	committed.GeneratedBy, committed.Note = "", ""
	fresh.Note = ""

	freshJSON, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the fresh bucket: %v", err)
	}
	committedJSON, err := json.MarshalIndent(committed, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the committed bucket: %v", err)
	}
	if string(freshJSON) != string(committedJSON) {
		t.Errorf("%s has drifted from a fresh regeneration - regenerate with:\n  go run ./tools/row-gen -schema-precedence\n\nCounts: committed has_identity_schema=%d reproduced=%d; fresh has_identity_schema=%d reproduced=%d",
			schemaPrecedenceJSONRel, committed.HasIdentitySchema, committed.ReproducedCount, fresh.HasIdentitySchema, fresh.ReproducedCount)
	}
}

// TestAnnotationsAgreeWithMismatches is Phase 3's machine check:
// annotations.json's rulings apply to real, current, admitted types with a
// real, current mismatch - see validateAnnotations (annotations.go) and
// annotations.json's own doc comment. Reads the committed artifact rather
// than a fresh regeneration, so this stays a fast, independent check.
func TestAnnotationsAgreeWithMismatches(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		t.Fatalf("loadAnnotations: %v", err)
	}
	ledger := loadCommittedMismatchLedger(t)

	if problems := validateAnnotations(ledger.Rows, annotations); len(problems) > 0 {
		for _, p := range problems {
			t.Error(p)
		}
	}
}

// loadCommittedMismatchLedger reads live/rowgen-mismatches.json as
// committed, the shared helper the drift test and
// TestAnnotationsAgreeWithMismatches both need.
func loadCommittedMismatchLedger(t *testing.T) mismatchLedger {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, mismatchesJSONRel))
	if err != nil {
		t.Fatalf("reading %s: %v (run: go run ./tools/row-gen -mismatches)", mismatchesJSONRel, err)
	}
	var l mismatchLedger
	if err := json.Unmarshal(data, &l); err != nil {
		t.Fatalf("decoding %s: %v", mismatchesJSONRel, err)
	}
	return l
}
