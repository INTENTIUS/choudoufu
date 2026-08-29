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

// unannotatedMismatchRatchetMax was rowgen-convergence's own downward
// ratchet, the "delete-me ratchet idiom" tools/mapping-gen's own
// unclassified-count ratchet already used for live/mapping.json: the
// highest live/rowgen-convergence.json's summary.unannotated_mismatches may
// be. Both are now entries in internal/live/harness; what follows is the
// provenance for this one's bound, kept because it is the evidence. This pass originally measured 170
// genuine mismatches over 571 admitted types, after merging
// ratify-sagemaker/ratify-governance/ratify-media. It was then bumped to
// 207 (125 scrape-gap) after a further eight concurrently-landed batches
// (ratify-data-movement, ratify-networking-advanced, ratify-databases,
// ratify-security, ratify-ec2-networking, ratify-iot,
// ratify-ai-location/stragglers/connect-euc among them) raised the admitted
// count to 652, none of them ever having passed through this convergence
// check before (it did not exist yet when they were ratified).
//
// Merging ratify-remainder (issue #65's REMAINDER batch, the long tail of
// services outside every concurrent batch's own scope) raised the admitted
// count again, 652 to 836 (184 more - the exact count
// live/e2e/estates/remainder/README.md's own "184 types admitted" gives),
// and, the same as every prior batch above, never having passed through
// this convergence check before, surfaces its own row-gen/DefaultTable
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
// floor read straight off live/rowgen-convergence.json's own committed
// summary, not a bump: the three batches that contributed to it are
// ratify-remainder (+184 admitted types), the eight concurrently-landed
// batches named above, and importdocs-widen (-26 mismatches).
//
// See tools/row-gen/annotations.json's own doc comment for why none of the
// 215 is annotated yet. Lower this constant to match
// live/rowgen-convergence.json's own committed count whenever a future
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
// The committed 128 is 107 genuine mismatches (live/rowgen-convergence.json's
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
// surfaced in the move. It gained a denominator - the convergence
// artifact's admitted_total - because the cheapest way to delete a ruling
// is to un-admit the type it names, which lowers the count while removing
// support, and nothing recorded that. And the const was measured stale for
// the second time in two days: the committed ledger carried 93 rulings
// against a ceiling of 95, so it had stopped bounding anything. The entry's
// bound is 93, taken from the measurement rather than from this comment.

// TestConvergenceArtifactMatchesCommitted is the drift half the ratchet
// test above deliberately does not do itself (mapping_gen_test.go's own
// TestUnclassifiedCountRatchet keeps that same separation, for the same
// reason its own doc comment gives: the ratchet stays a working, cheap
// check on its own even if this drift test's shape changes later). This
// test regenerates the comparison from the checked-out sources - the same
// classifyAll pipeline -convergence uses - and requires it to match the
// committed artifact byte-for-byte modulo JSON formatting, so a change to
// classify.go, importprecedence.go or annotations.json without a
// regeneration of live/rowgen-convergence.json fails the build instead of
// silently drifting.
func TestConvergenceArtifactMatchesCommitted(t *testing.T) {
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
	fresh := buildConvergence(loadEmittedTableForTest(t, proposals), proposals, annotations)
	fresh.SchemaReproduces = buildSchemaReproducesBucket(loadRatifiedForTest(t), loadImportGrammarForTest(t))
	survey, err := loadSurvey(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("loadSurvey: %v", err)
	}
	fresh.EvidenceOnlySchema = buildEvidenceOnlySchemaBucket(proposals, survey)

	committed := loadCommittedConvergence(t)

	freshJSON, err := json.MarshalIndent(fresh, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the fresh comparison: %v", err)
	}
	committedJSON, err := json.MarshalIndent(committed, "", "  ")
	if err != nil {
		t.Fatalf("marshaling the committed comparison: %v", err)
	}
	if string(freshJSON) != string(committedJSON) {
		t.Errorf("%s has drifted from a fresh regeneration - regenerate with:\n  go run ./tools/row-gen -convergence\n\nSummary drift: committed=%+v fresh=%+v",
			convergenceJSONRel, committed.Summary, fresh.Summary)
	}
}

// TestAnnotationsAgreeWithMismatches is Phase 3's machine check:
// annotations.json's rulings apply to real, current, admitted types with a
// real, current mismatch - see validateAnnotations (annotations.go) and
// annotations.json's own doc comment. Reads the committed artifact rather
// than a fresh regeneration, the same choice TestUnannotatedMismatchRatchet
// makes, so this stays a fast, independent check.
func TestAnnotationsAgreeWithMismatches(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	annotations, err := loadAnnotations(filepath.Join(root, annotationsJSONRel))
	if err != nil {
		t.Fatalf("loadAnnotations: %v", err)
	}
	art := loadCommittedConvergence(t)

	if problems := validateAnnotations(art, annotations); len(problems) > 0 {
		for _, p := range problems {
			t.Error(p)
		}
	}
}

// loadCommittedConvergence reads live/rowgen-convergence.json as committed,
// the shared helper TestUnannotatedMismatchRatchet, TestAnnotationsAgree-
// WithMismatches and TestConvergenceArtifactMatchesCommitted all need.
func loadCommittedConvergence(t *testing.T) convergenceArtifact {
	t.Helper()
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(root, convergenceJSONRel))
	if err != nil {
		t.Fatalf("reading %s: %v (run: go run ./tools/row-gen -convergence)", convergenceJSONRel, err)
	}
	var art convergenceArtifact
	if err := json.Unmarshal(data, &art); err != nil {
		t.Fatalf("decoding %s: %v", convergenceJSONRel, err)
	}
	return art
}

// TestDeriveCompositeWithSeparator_OrderFromString pins
// importprecedence.go's central safety property: argument order is always
// recovered from the documented example string's own left-to-right shape,
// never trusted from the candidate list's own order - the property that
// keeps aws_api_gateway_method (registry/grammar argument order
// alphabetical, string order reversed) from being proposed with the wrong
// order, and resolves aws_networkmanager_link_association's separator and
// order correctly even though the registry's own primaryIdentifier order
// (GlobalNetworkId, DeviceId, LinkId) disagrees with the documented string
// (global_network_id, link_id, device_id).
func TestDeriveCompositeWithSeparator_OrderFromString(t *testing.T) {
	tests := []struct {
		name       string
		example    string
		sep        string
		candidates []string
		wantOK     bool
		wantOrder  []string
	}{
		{
			name:       "order recovered from the string, not the candidate list",
			example:    "global-network-0d47f6t230mz46dy4,link-444555aaabbb11223,device-07f6fd08867abc123",
			sep:        ",",
			candidates: []string{"global_network_id", "device_id", "link_id"}, // registry order: device before link
			wantOK:     true,
			wantOrder:  []string{"global_network_id", "link_id", "device_id"}, // string order: link before device
		},
		{
			name:       "arity mismatch fails closed (the aws_route trap)",
			example:    "rtb-656C65616E6F72_10.42.0.0/16",
			sep:        "_",
			candidates: []string{"destination_cidr_block", "destination_ipv6_cidr_block", "destination_prefix_list_id", "route_table_id"},
			wantOK:     false,
		},
		{
			name:       "opaque placeholder values carry no name token: fails closed rather than guessing",
			example:    "12345abcde/67890fghij/GET",
			sep:        "/",
			candidates: []string{"http_method", "resource_id", "rest_api_id"},
			wantOK:     false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dc, ok := deriveCompositeWithSeparator(tt.example, tt.sep, tt.candidates)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v (dc=%+v)", ok, tt.wantOK, dc)
			}
			if !ok {
				return
			}
			if len(dc.ArgsInOrder) != len(tt.wantOrder) {
				t.Fatalf("ArgsInOrder = %v, want %v", dc.ArgsInOrder, tt.wantOrder)
			}
			for i := range dc.ArgsInOrder {
				if dc.ArgsInOrder[i] != tt.wantOrder[i] {
					t.Errorf("ArgsInOrder[%d] = %q, want %q (full: %v)", i, dc.ArgsInOrder[i], tt.wantOrder[i], dc.ArgsInOrder)
				}
			}
		})
	}
}

// TestLabelForOpaqueValue pins the ARN-vs-short-id label rule
// tryArnVsIDOverride and tryOpaqueOverride both share.
func TestLabelForOpaqueValue(t *testing.T) {
	tests := []struct {
		example      string
		wantSyntax   string
		wantIdentity string
	}{
		{"arn:aws:networkmanager::123456789012:device/global-network-x/device-y", "ARN", "arn"},
		{"svc-06728e2357ea55f8a", "ID", "id"},
		{"s-12345678", "ID", "id"},
	}
	for _, tt := range tests {
		syntax, attr := labelForOpaqueValue(tt.example)
		if syntax != tt.wantSyntax || attr != tt.wantIdentity {
			t.Errorf("labelForOpaqueValue(%q) = (%q, %q), want (%q, %q)", tt.example, syntax, attr, tt.wantSyntax, tt.wantIdentity)
		}
	}
}
