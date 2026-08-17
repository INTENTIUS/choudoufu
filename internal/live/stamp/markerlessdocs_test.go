// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/pins"
)

// This file pins the half of the markerless admission veto that nothing was
// pinning: SERVER-ASSIGNMENT (issue #257).
//
// tools/row-gen/markerless.go vetoes a type on a conjunction - untaggable AND
// server-assigned - and retracts it from the admission table. Both halves are
// load-bearing and they are not equally guarded. Untaggability is
// live/survey-full.json's signals.taggable and is checked against the table
// from two directions already (TestPinnedTaggabilityMatchesTheSurvey).
// Server-assignment was checked by nothing at all.
//
// TestMarkerOnlyUnconditionalBucketIsEmptyByVeto said in its own words that it
// closed that gap, and 6bb23bcbf8's commit message repeated the claim. It did
// not. Its population was "untaggable AND on the roster", and because
// markerlessRoster returns false for every taggable type out of the same
// survey file, that set IS the roster, 150 of 150. It never read
// ServerAssigned from anywhere. An adversarial audit replaced markerless()'s
// body with `if taggable { return false }; return true` - deleting the
// server-assignment leg outright, retracting 217 further rows and taking
// identity resolution from 890 to 673 - and that test passed.
//
// # What an independent source can be here
//
// Not the identity table: a vetoed type is retracted from it by construction,
// so there is no ratified row left to read. Not the survey: it carries
// taggability and nothing about who mints an ID. What is left inside the tree
// is live/import-grammar.json, tools/importdocs-gen's scrape of the provider's
// own documentation, which attributes each named segment of a type's
// documented import ID to the Argument Reference, to the Attribute Reference,
// or to nothing. That is the provider's prose answering "can this ID be
// written down from a configuration", produced by a different generator from a
// different input than the roster.
//
// # The assertion, and why it is one-directional
//
// Forwards only: a type the veto names must not be one the documentation
// REFUTES. Refutation is the strong reading and it is deliberately narrow -
// every named segment of the documented import ID is attributed to an argument
// AND names a bullet in the resource's own top-level Argument Reference. The
// second clause is not decoration. tools/importdocs-gen/soleid.go attributes a
// lone segment against arguments at any block depth on purpose (it is a
// refutation set there, so width is safety), which makes
// aws_ecr_replication_configuration's `registry_id` read as an argument when
// the only `registry_id` bullet on the page belongs to a nested destination
// block. Requiring the top-level bullet drops that misread and every one like
// it.
//
// The backwards direction is not assertable and saying why is more useful than
// asserting a weaker version of it. Seven untaggable types carry a
// server-attributed segment and are not on the roster; two are admitted with a
// ratified row that outranks the docs (markerless.go's own comment describes
// this case and measures six of them), and the other five are in neither
// ledger - unratified, which is tools/row-gen/rejected.json's business and not
// this veto's. A backwards assertion would therefore need a hand list of five
// type names, which is the thing this repository does not do.
//
// # Why the forwards direction is not vacuous
//
// Measured at 2d9a244a9f, over the 852 untaggable types in
// live/survey-full.json: 196 are doc-refuted by the rule above, and the
// roster's 150 hold none of them. So refutation is ordinary among the 702
// untaggable types the veto does not name - 27.9% of them - and absent from
// the 150 it does. That is the agreement being asserted, and under the audit's
// mutation the roster becomes all 852 and takes every one of the 196 with it.

// markerlessDocCorroborationFloor is the anti-tamper leg, in the spirit of
// live/identity_golden_pin_test.go's identityGoldenSweepFloor.
//
// "No roster member is refuted" is satisfiable by refuting nothing: narrow the
// predicate, mis-key the artifact, decode the wrong field, and the check
// passes while seeing zero. So the same predicate's positive half has to keep
// finding the roster's own members, and this is the number that may not fall
// silently. It was 73 of 150 when written.
//
// It is a floor rather than an exact pin because the roster is expected to
// SHRINK - every type an admission batch ratifies leaves it - and a count that
// has to be re-pinned by every unrelated batch is a count people re-pin without
// reading. If the roster genuinely shrinks past this, lower it in its own
// commit that says so, not in the commit that shrank it.
const markerlessDocCorroborationFloor = 40

// docSegmentSourceArgument and friends are tools/importdocs-gen/parse.go's own
// IDPart.Source vocabulary, re-declared here for the reason every artifact
// reader in this fork re-declares its shape: that package is main and cannot
// be imported.
const (
	docSegmentSourceArgument  = "argument"
	docSegmentSourceAttribute = "attribute"
	docSegmentSourceOwnID     = "own-id"
)

// docVerdict is what live/import-grammar.json says about one type's documented
// import ID.
type docVerdict int

const (
	// docSilent: the scrape attributed no segment, or attributed some to
	// neither side. The majority, and evidence of nothing.
	docSilent docVerdict = iota
	// docCorroborates: at least one segment is the doc's own Attribute
	// Reference export or its own prose-named identifier - a value no
	// configuration supplies.
	docCorroborates
	// docRefutes: every named segment is an argument the resource's own
	// top-level Argument Reference lists. The ID is writable from a
	// configuration, so the server does not mint the identity.
	docRefutes
)

// TestMarkerlessVetoIsNotRefutedByTheImportDocs is the guard #257 asked for:
// the markerless roster read against a source that did not produce it.
//
// It fails in three directions. A roster member the docs refute is the veto
// over-reaching, which is the audit's mutation and the reason this exists. A
// roster member live/import-grammar.json has never heard of means the two
// artifacts are describing different providers and the comparison is not one.
// Corroboration falling through the floor means the predicate stopped seeing
// the shape, which is how "nothing was refuted" becomes true by blindness.
func TestMarkerlessVetoIsNotRefutedByTheImportDocs(t *testing.T) {
	if len(identity.MarkerlessTypes) == 0 {
		t.Fatal("identity.MarkerlessTypes is empty; there is no veto left to corroborate, and every assertion below would pass over nothing")
	}
	grammar := readImportGrammar(t)

	var refuted, corroborated, silent, missing []string
	for name := range identity.MarkerlessTypes {
		row, ok := grammar[name]
		if !ok {
			missing = append(missing, name)
			continue
		}
		switch docReadsSegments(row) {
		case docRefutes:
			refuted = append(refuted, name+" ("+row.documentedID()+")")
		case docCorroborates:
			corroborated = append(corroborated, name)
		default:
			silent = append(silent, name)
		}
	}
	sort.Strings(refuted)
	sort.Strings(missing)

	if len(missing) > 0 {
		t.Errorf("%d vetoed type(s) have no row in live/import-grammar.json: %v\n"+
			"The roster is derived from live/survey-full.json and this comparison assumes the two artifacts cover the same provider release; they do not.",
			len(missing), missing)
	}
	if len(refuted) > 0 {
		t.Errorf("%d type(s) are vetoed as server-assigned, but the provider's own documentation composes their whole import ID out of top-level arguments:\n%s\n"+
			"tools/row-gen/markerless.go retracts these from the admission table on the ground that nothing can find the object again. A configuration that names the object contradicts that.",
			len(refuted), indentedSample(refuted))
	}
	if len(corroborated) < markerlessDocCorroborationFloor {
		t.Errorf("only %d of %d vetoed types are corroborated by a server-supplied import segment, below the floor of %d.\n"+
			"The refutation assertion above is satisfied by refuting nothing, so this is the leg that is not allowed to fall quietly. See markerlessDocCorroborationFloor.",
			len(corroborated), len(identity.MarkerlessTypes), markerlessDocCorroborationFloor)
	}

	t.Logf("markerless roster %d: %d corroborated, %d refuted, %d silent, %d absent from the grammar (provider %s)",
		len(identity.MarkerlessTypes), len(corroborated), len(refuted), len(silent), len(missing), pins.AWSProviderVersion)
}

// TestImportDocsRefuteUntaggableTypesTheVetoDoesNotName is the mutation
// detector stated as its own assertion, so that the test above cannot pass by
// having nothing to disagree with.
//
// The refutation predicate must find refutations SOMEWHERE in the population
// the veto draws from. If it finds none among untaggable types generally, then
// finding none among the vetoed ones says nothing, and
// TestMarkerlessVetoIsNotRefutedByTheImportDocs has become a test of the
// predicate's silence rather than of the roster.
//
// This is the control in the experiment. It is over untaggable types the
// roster does NOT name, so it cannot be satisfied by the roster at all.
func TestImportDocsRefuteUntaggableTypesTheVetoDoesNotName(t *testing.T) {
	survey := readSurveyTaggable(t)
	grammar := readImportGrammar(t)

	outside, refutedOutside := 0, 0
	for name, taggable := range survey {
		if taggable {
			continue
		}
		if _, vetoed := identity.MarkerlessTypes[name]; vetoed {
			continue
		}
		outside++
		if row, ok := grammar[name]; ok && docReadsSegments(row) == docRefutes {
			refutedOutside++
		}
	}

	if outside == 0 {
		t.Fatal("live/survey-full.json reports no untaggable type outside the veto at all; the control population is empty and this test compares nothing")
	}
	// A fifth is comfortably under the 27.9% measured at 2d9a244a9f (196 of
	// 702) and far above zero, so it fails on a predicate that stopped
	// matching rather than on the provider documenting a few more IDs one way
	// or the other.
	if want := outside / 5; refutedOutside < want {
		t.Errorf("the documentation refutes server-assignment for %d of the %d untaggable types the veto does not name; want at least %d.\n"+
			"This is the control for TestMarkerlessVetoIsNotRefutedByTheImportDocs: if the predicate refutes almost nothing anywhere, its silence over the roster is not evidence that the roster is right.",
			refutedOutside, outside, want)
	}
	t.Logf("%d of %d untaggable non-vetoed types are doc-refuted", refutedOutside, outside)
}

// docReadsSegments applies the reading this file's doc comment describes to one
// grammar row. It names no resource type and reads no list.
func docReadsSegments(row importGrammarDocRow) docVerdict {
	segments := row.segments()
	if len(segments) == 0 {
		return docSilent
	}
	topLevel := make(map[string]bool, len(row.ArgumentReference))
	for _, a := range row.ArgumentReference {
		topLevel[foldDocName(a.Name)] = true
	}

	allArguments := true
	for _, seg := range segments {
		switch seg.Source {
		case docSegmentSourceAttribute, docSegmentSourceOwnID:
			// One server-supplied segment is enough: the whole ID cannot be
			// written down without it.
			return docCorroborates
		case docSegmentSourceArgument:
			if !topLevel[foldDocName(seg.Token)] {
				// Attributed to an argument the resource's own page does not
				// list at top level - tools/importdocs-gen's any-depth
				// refutation set reaching a nested block's bullet. Safe as a
				// refusal to call the segment server-minted; not a statement
				// that a configuration supplies it.
				allArguments = false
			}
		default:
			allArguments = false
		}
	}
	if allArguments {
		return docRefutes
	}
	return docSilent
}

// indentedSample renders a failure's evidence one line per type, capped. The
// cap is not cosmetic: the mutation this test exists to catch produces 196 of
// these, and 196 entries on one line is a paragraph nobody reads, in a test
// whose whole subject is a claim nobody checked.
func indentedSample(names []string) string {
	const limit = 12
	shown := names
	var tail string
	if len(shown) > limit {
		shown, tail = shown[:limit], fmt.Sprintf("\n  ... and %d more", len(names)-limit)
	}
	return "  " + strings.Join(shown, "\n  ") + tail
}

// foldDocName is tools/row-gen/importgrammar.go's normalizeName: prose segment
// names and bullet names are compared on their letters and digits alone, so
// "Analyzer Name" and `analyzer_name` are one name.
func foldDocName(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// importGrammarDocRow is the slice of live/import-grammar.json this file reads.
type importGrammarDocRow struct {
	TFType            string           `json:"tf_type"`
	ImportIDExample   string           `json:"import_id_example"`
	IDParts           []docIDPart      `json:"id_parts"`
	SoleIDPart        *docIDPart       `json:"sole_id_part"`
	ArgumentReference []docArgumentRef `json:"argument_reference"`
}

type docIDPart struct {
	Token  string `json:"token"`
	Source string `json:"source"`
}

type docArgumentRef struct {
	Name string `json:"name"`
}

// segments is every named segment of the documented import ID over both
// arities the scrape records: IDParts for a composite, SoleIDPart for a
// one-segment ID. Reading only one of the two is how the veto's own doc leg
// missed the single-segment population before #249.
func (r importGrammarDocRow) segments() []docIDPart {
	out := append([]docIDPart(nil), r.IDParts...)
	if r.SoleIDPart != nil {
		out = append(out, *r.SoleIDPart)
	}
	return out
}

// documentedID renders the evidence a failure needs: the doc's own example and
// the tokens it attributed, so a reader can go to the page rather than to the
// artifact.
func (r importGrammarDocRow) documentedID() string {
	tokens := make([]string, 0, len(r.IDParts)+1)
	for _, seg := range r.segments() {
		tokens = append(tokens, seg.Token)
	}
	return r.ImportIDExample + " = " + strings.Join(tokens, "/")
}

// readImportGrammar reads live/import-grammar.json keyed by resource type, and
// fails rather than degrading if the artifact is missing, unparseable, empty,
// or pinned to a different provider release from every other measurement here -
// the same contract readSurveyTaggable states for the survey, because these two
// artifacts are compared against each other and a version skew between them
// would be read as a disagreement about types.
func readImportGrammar(t *testing.T) map[string]importGrammarDocRow {
	t.Helper()

	path := filepath.Join(flocitest.RepoRoot(t), "live", "import-grammar.json")
	content, err := os.ReadFile(path) //nolint:gosec // a fixed path in the checkout
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}

	var artifact struct {
		Provider        string                `json:"provider"`
		ProviderVersion string                `json:"provider_version"`
		Rows            []importGrammarDocRow `json:"rows"`
	}
	if err := json.Unmarshal(content, &artifact); err != nil {
		t.Fatalf("decoding %s: %v", path, err)
	}
	if artifact.ProviderVersion != pins.AWSProviderVersion {
		t.Fatalf("live/import-grammar.json was scraped from %s %s, but the roster it is compared against comes from %s; regenerate it before trusting this comparison",
			artifact.Provider, artifact.ProviderVersion, pins.AWSProviderVersion)
	}
	if len(artifact.Rows) == 0 {
		t.Fatal("live/import-grammar.json carries no rows")
	}

	out := make(map[string]importGrammarDocRow, len(artifact.Rows))
	for _, row := range artifact.Rows {
		out[row.TFType] = row
	}
	return out
}
