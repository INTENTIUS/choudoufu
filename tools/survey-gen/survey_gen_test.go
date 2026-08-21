// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestSurveyJSONMatchesProviderSchemas regenerates live/survey.json
// from the pinned provider's live schemas and diffs it against the
// committed artifact. A mismatch means either the committed file is stale
// (rerun `go run ./tools/survey-gen` and review the diff) or the provider
// release changed under the pin.
//
// It is gated with the floci tier because it needs a terraform binary and a
// provider download, but it starts no emulator: a schema read needs no
// cloud (the same note TestTaggableSetAgainstRealSchemas carries).
//
//	TF_FLOCI_TEST=1 go test ./tools/survey-gen/ -run TestSurveyJSONMatchesProviderSchemas -v
func TestSurveyJSONMatchesProviderSchemas(t *testing.T) {
	flocitest.Gate(t, "survey generation")
	flocitest.RequireBinary(t, defaultInitBin)

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	roster, err := readRoster(filepath.Join(root, surveyMDRel))
	if err != nil {
		t.Fatalf("reading the roster from %s: %v", surveyMDRel, err)
	}

	schemas, importable, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring the provider schemas: %v", err)
	}

	got, err := buildSurvey(schemas, rosterTypes(roster), testServiceOf, testEnumeration, importable).marshal()
	if err != nil {
		t.Fatalf("marshaling the regenerated survey: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("reading the committed %s: %v", surveyJSONRel, err)
	}

	// Accepted is a human ratification stamp the generator only writes
	// with -accept (issue #37, increment 1); buildSurvey itself never sets
	// it, so strip it from the committed file before comparing - this test
	// is about whether the rows are stale, not whether someone accepted
	// them.
	var wantS Survey
	if err := json.Unmarshal(want, &wantS); err != nil {
		t.Fatalf("decoding the committed %s: %v", surveyJSONRel, err)
	}
	wantS.Accepted = ""
	wantCanonical, err := wantS.marshal()
	if err != nil {
		t.Fatalf("re-marshaling the committed %s: %v", surveyJSONRel, err)
	}
	if bytes.Equal(got, wantCanonical) {
		return
	}

	// Name the rows that moved rather than dumping two blobs.
	var gotS Survey
	if err := json.Unmarshal(got, &gotS); err != nil {
		t.Fatal(err)
	}
	wantRows := map[string]string{}
	for _, r := range wantS.Types {
		wantRows[r.Type] = rowJSON(t, r)
	}
	for _, g := range gotS.Types {
		w, ok := wantRows[g.Type]
		if !ok {
			t.Errorf("%s: regenerated but absent from the committed file", g.Type)
			continue
		}
		delete(wantRows, g.Type)
		if gj := rowJSON(t, g); gj != w {
			t.Errorf("%s drifted:\n  committed:   %s\n  regenerated: %s", g.Type, w, gj)
		}
	}
	for typeName := range wantRows {
		t.Errorf("%s: committed but no longer regenerated", typeName)
	}
	if gotS.Counts != wantS.Counts {
		t.Errorf("counts drifted: committed %+v, regenerated %+v", wantS.Counts, gotS.Counts)
	}
	t.Errorf("%s is stale; rerun `go run ./tools/survey-gen` and review the diff", surveyJSONRel)
}

// TestSurveyFullJSONMatchesProviderSchemas is
// TestSurveyJSONMatchesProviderSchemas's sibling for live/survey-full.json.
//
// It exists because that test alone left a hole: it regenerates and diffs
// only the curated 68-type live/survey.json, so nothing here ever ran a
// live regeneration of the 1699-type -all roster and compared it byte for
// byte against what is committed. drift_test.go's
// TestSurveyArtifactsMatchTheirExpectations covers survey-full.json too,
// but only against a hand-maintained Paths/Counts table in the same file -
// an expectation kept in step by hand alongside the artifact, not derived
// from a fresh run - so a type whose classification moved without a
// regeneration (aws_ecs_capacity_provider: marker -> account-derived,
// after #150 gave it region/account-id-bearing IdentityAttrs) passed that
// gate every time, exactly the #169 shape both tests exist to catch. This
// one would have failed on the spot.
//
//	TF_FLOCI_TEST=1 go test ./tools/survey-gen/ -run TestSurveyFullJSONMatchesProviderSchemas -v
func TestSurveyFullJSONMatchesProviderSchemas(t *testing.T) {
	flocitest.Gate(t, "survey generation")
	flocitest.RequireBinary(t, defaultInitBin)

	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	schemas, importable, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring the provider schemas: %v", err)
	}

	full := buildSurvey(schemas, allResourceTypeNames(schemas), testServiceOf, testEnumeration, importable)
	full.GeneratedBy = "tools/survey-gen (go run ./tools/survey-gen -all)"
	got, err := full.marshal()
	if err != nil {
		t.Fatalf("marshaling the regenerated %s: %v", surveyFullJSONRel, err)
	}
	want, err := os.ReadFile(filepath.Join(root, surveyFullJSONRel))
	if err != nil {
		t.Fatalf("reading the committed %s: %v", surveyFullJSONRel, err)
	}

	// Accepted is a human ratification stamp the generator only writes
	// with -accept; buildSurvey never sets it, so strip it from the
	// committed file before comparing - this test is about whether the
	// rows are stale, not whether someone accepted them.
	var wantS Survey
	if err := json.Unmarshal(want, &wantS); err != nil {
		t.Fatalf("decoding the committed %s: %v", surveyFullJSONRel, err)
	}
	wantS.Accepted = ""
	wantCanonical, err := wantS.marshal()
	if err != nil {
		t.Fatalf("re-marshaling the committed %s: %v", surveyFullJSONRel, err)
	}
	if bytes.Equal(got, wantCanonical) {
		return
	}

	// Name the rows that moved rather than dumping two 1699-row blobs.
	var gotS Survey
	if err := json.Unmarshal(got, &gotS); err != nil {
		t.Fatal(err)
	}
	wantRows := map[string]string{}
	for _, r := range wantS.Types {
		wantRows[r.Type] = rowJSON(t, r)
	}
	for _, g := range gotS.Types {
		w, ok := wantRows[g.Type]
		if !ok {
			t.Errorf("%s: regenerated but absent from the committed file", g.Type)
			continue
		}
		delete(wantRows, g.Type)
		if gj := rowJSON(t, g); gj != w {
			t.Errorf("%s drifted:\n  committed:   %s\n  regenerated: %s", g.Type, w, gj)
		}
	}
	for typeName := range wantRows {
		t.Errorf("%s: committed but no longer regenerated", typeName)
	}
	if gotS.Counts != wantS.Counts {
		t.Errorf("counts drifted: committed %+v, regenerated %+v", wantS.Counts, gotS.Counts)
	}
	t.Errorf("%s is stale; rerun `go run ./tools/survey-gen -all` and review the diff", surveyFullJSONRel)
}

// rowJSON renders one row canonically for comparison and for failure
// messages (Row carries a pointer field, so == would compare addresses).
func rowJSON(t *testing.T, r Row) string {
	t.Helper()
	b, err := json.Marshal(r)
	if err != nil {
		t.Fatalf("marshaling a row: %v", err)
	}
	return string(b)
}

// testLogWriter routes the generator's progress lines through t.Logf.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", bytes.TrimRight(p, "\n"))
	return len(p), nil
}

// ---------------------------------------------------------------------------
// The committed artifact against the hand-written table
// ---------------------------------------------------------------------------

// pathException is one type where the mechanical classifier and SURVEY.md's
// hand table disagree for a reason that is a known classifier limitation
// (or a disagreement SURVEY.md itself documents). Visible, not silent: the
// test fails on a disagreement with no entry here, on an entry whose sides
// no longer match what the two files say, and on an entry no disagreement
// uses anymore.
type pathException struct {
	// hand and generated are the two Path claims, exactly as each file
	// carries them.
	hand, generated string

	// reason is the one-line explanation.
	reason string

	// cohort is which of the five groups this exception measures - one of
	// the cohortXxx constants below, and live/SURVEY.md's own "What the
	// classifier does not settle" section names the same five with the
	// same sizes (TestExceptionCohortCounts holds the two together).
	cohort string

	// tracking is the two-way ratchet issue #37's increment 4 adds,
	// following chant's KNOWN_FAILURES convention: either a
	// "choudoufu#NN" issue whose resolution should remove this entry, or
	// the literal "permanent" for a disagreement that is deliberate fork
	// design and will not close by itself. TestExceptionTracking enforces
	// the format; TestSurveyJSONAgainstHandTable's existing staleness
	// checks (an exception no disagreement uses anymore, or a
	// disagreement with no exception) are what actually retire an entry
	// once its tracking issue lands - this field only says where to look.
	tracking string
}

// The five pathException cohorts, named identically to live/SURVEY.md's
// "What the classifier does not settle" table so the doc and this file
// cannot drift apart without TestExceptionCohortCounts saying so.
const (
	// cohortNamePrefix: the type's identifying argument (name, bucket,
	// statement_id...) is Optional+Computed in the legacy-SDK schema
	// because a *_prefix alternative exists, and the strict client-named
	// rule deliberately stops at required arguments - accepting
	// Optional+Computed would admit aws_vpc by its id
	// (internal/live/identity/doc.go). The hand row knows the value is
	// client-chosen; the schema cannot prove it.
	cohortNamePrefix = "name-prefix idiom"

	// cohortAccountDerived (SURVEY.md flags F3-F4): the provider's
	// required import attribute is an arn or url that embeds
	// account/region, so the schema says server-assigned while the survey
	// keeps the config-name judgment and flags the gap.
	cohortAccountDerived = "account-derived import identity"

	// cohortDocsTier: v6.58.0 ships no identity schema, so the hand row
	// read the provider's documented import grammar - a source the
	// schema-only classifier does not have.
	cohortDocsTier = "docs tier"

	// cohortForkWrinkle: server-composed identities the path vocabulary
	// cannot split (aws_ecs_task_definition, aws_route_table_association,
	// aws_sns_topic_subscription).
	cohortForkWrinkle = "fork-wiring wrinkle"

	// cohortParentComponent: aws_route53_record, where the classifier and
	// flag F5 agree that zone_id is a parent's server-assigned Z-ID and
	// the survey keeps client-named anyway.
	cohortParentComponent = "parent component"
)

// pathExceptions is the full disagreement list between the hand table and
// the schema-derived classification, one documented entry per type, grouped
// into the five cohorts above.
var pathExceptions = map[string]pathException{
	// --- name-prefix idiom (Optional+Computed identifying argument) ---
	//
	// All twelve wait on the same upstream fact: the provider's identity
	// schema marking the identifying argument required-for-import instead
	// of optional. That is the opentofu#2854 gap tracked here as
	// choudoufu#22.
	"aws_cloudwatch_event_rule": {hand: pathClientNamed, generated: pathMarker, reason: "name is Optional+Computed (name_prefix idiom); strict rule cannot prove client-naming, falls to marker", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_cloudwatch_log_group":  {hand: pathClientNamed, generated: pathMarker, reason: "name is Optional+Computed (name_prefix idiom); falls to marker", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_db_subnet_group":       {hand: pathClientNamed, generated: pathMarker, reason: "name is Optional+Computed (name_prefix idiom); falls to marker", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_ecs_service":           {hand: pathClientNamed, generated: pathMarker, reason: "cluster and name are Optional+Computed in the schema; falls to marker", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_eks_node_group":        {hand: pathClientNamed, generated: pathMarker, reason: "cluster_name/node_group_name are Optional+Computed (node_group_name_prefix idiom); falls to marker", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_iam_instance_profile":  {hand: pathClientNamed, generated: pathMarker, reason: "name is Optional+Computed (name_prefix idiom); falls to marker", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_iam_role":              {hand: pathClientNamed, generated: pathMarker, reason: "name is Optional+Computed (name_prefix idiom); falls to marker", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_s3_bucket":             {hand: pathClientNamed, generated: pathMarker, reason: "bucket is Optional+Computed (bucket_prefix idiom), the archetype in identity/doc.go; falls to marker", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_autoscaling_group":     {hand: pathClientNamed, generated: pathEnumerableUnbindable, reason: "name is Optional+Computed (name_prefix idiom), and tags are tag blocks rather than a tags map, so the fallback is enumerable-unbindable", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_iam_role_policy":       {hand: pathClientNamed, generated: pathEnumerableUnbindable, reason: "name is Optional+Computed (name_prefix idiom); untaggable, falls to enumerable-unbindable", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_kms_alias":             {hand: pathClientNamed, generated: pathEnumerableUnbindable, reason: "name is Optional+Computed (name_prefix idiom); untaggable, falls to enumerable-unbindable", cohort: cohortNamePrefix, tracking: "choudoufu#22"},
	"aws_lambda_permission":     {hand: pathClientNamed, generated: pathEnumerableUnbindable, reason: "statement_id is Optional+Computed (statement_id_prefix idiom); untaggable, falls to enumerable-unbindable", cohort: cohortNamePrefix, tracking: "choudoufu#22"},

	// --- account-derived import identity (SURVEY.md flags F3-F4) ---
	//
	// F1 (aws_sqs_queue) and F2 (aws_sns_topic) are no longer here: the
	// identity table builds each one's import identity out of the name plus
	// the run's account and region, the classifier reads that assertion, and
	// both files say account-derived for both. The two below are the ones
	// the mechanism does not carry all the way to a wired row, each for its
	// own reason. The first is blocked on the floci emulator gap
	// choudoufu#26 tracks, not on anything upstream; the second is
	// permanent, since no account/region template will ever reconstruct a
	// server-minted secret suffix.
	"aws_iam_policy":            {hand: pathClientNamed, generated: pathMarker, reason: "required import attribute is the policy arn (flag F3); the mechanism would build it, but floci's iam:GetPolicy omits Tags so the row cannot be proven live (choudoufu#26) and it is not wired", cohort: cohortAccountDerived, tracking: "choudoufu#26"},
	"aws_secretsmanager_secret": {hand: pathClientNamed, generated: pathMarker, reason: "required import attribute is the arn with a six-character server-generated suffix (flag F4), which no account/region template reconstructs; deferred to the marker path the classifier already reads off its taggability", cohort: cohortAccountDerived, tracking: "permanent"},

	// --- docs tier: no identity schema in v6.58.0 ---
	//
	// All five wait on the same upstream fact as the name-prefix cohort: a
	// first identity schema for the type. Tracked the same way,
	// choudoufu#22.
	"aws_ecs_cluster":        {hand: pathClientNamed, generated: pathMarker, reason: "no identity schema; hand row read the documented import grammar (name), classifier falls back to taggability", cohort: cohortDocsTier, tracking: "choudoufu#22"},
	"aws_key_pair":           {hand: pathClientNamed, generated: pathMarker, reason: "no identity schema; hand row read the documented import grammar (key_name), classifier falls back to taggability", cohort: cohortDocsTier, tracking: "choudoufu#22"},
	"aws_db_parameter_group": {hand: pathClientNamed, generated: pathMarker, reason: "no identity schema; hand row read the documented import grammar (name), classifier falls back to taggability", cohort: cohortDocsTier, tracking: "choudoufu#22"},
	"aws_iam_group":          {hand: pathClientNamed, generated: pathEnumerableUnbindable, reason: "no identity schema; hand row read the documented import grammar (name), classifier falls back to enumeration - Cloud Control lists AWS::IAM::Group", cohort: cohortDocsTier, tracking: "choudoufu#22"},

	// aws_cloudfront_origin_access_control used to sit here as
	// {hand: "list + content match" (the token then in use), generated: moves to Ops}, tracked to
	// choudoufu#22 as if a missing identity schema were the cause. It was
	// not: the hand row was right and the classifier was wrong, because
	// the classifier's enumeration question read only the provider's own
	// native list resource while internal/live/discovery has consulted the
	// mapped CFN type's Cloud Control list handler since #47. AWS::CloudFront::
	// OriginAccessControl lists with no scoping input, so the two agree now
	// and the entry is gone. No identity schema appeared to make that
	// happen, which is why the tracking issue would never have retired it.

	// aws_eip used to sit here too, as {hand: list + content match,
	// generated: marker}, marked permanent on the reasoning that the hand
	// row "shows the fork's list+content wiring". It showed no such thing.
	// What binds an eip is internal/live/discovery's bindCountBySlot, and a
	// slot is a TAG - the tofu-slot marker, which the row's own Identity
	// cell named all along. Three independent sources said marker (the
	// generator, the summary override that counted it as marker, and the
	// row's own identity prose) against one Path cell that said otherwise,
	// so the 2026-08-17 rename settled it as marker and the disagreement
	// disappeared. The summary counts do not move: summaryOverrides was
	// already tallying it under marker, and that override is gone with this
	// entry.

	// --- wrinkles SURVEY.md or c02d492 already records ---
	//
	// All three are permanent: server-composed identities the path
	// vocabulary was never meant to split further,
	// aws_route_table_association's association id being the c02d492 case
	// in point - its hand row keeps the documented subnet+route-table
	// import string on purpose.
	"aws_ecs_task_definition":     {hand: pathParentDerived, generated: pathMarker, reason: "identity is family plus a server-assigned revision; the classifier has no parent-derived-with-server-component shape and falls to marker", cohort: cohortForkWrinkle, tracking: "permanent"},
	"aws_route_table_association": {hand: pathParentDerived, generated: pathEnumerableUnbindable, reason: "the provider identifies an association by its rtbassoc- id (the c02d492 divergence); the hand row keeps the documented subnet+route-table import string", cohort: cohortForkWrinkle, tracking: "permanent"},
	"aws_sns_topic_subscription":  {hand: pathParentDerived, generated: pathEnumerableUnbindable, reason: "identity is the subscription arn, the parent topic arn plus a server uuid (flag F6); the schema sees only a server-assigned arn", cohort: cohortForkWrinkle, tracking: "permanent"},

	// --- parent component the survey flags but does not reclassify ---
	//
	// A permanent editorial choice, not a wait on upstream: the survey
	// keeps client-named for zone_id on purpose, so no schema change would
	// remove this entry by itself.
	"aws_route53_record": {hand: pathClientNamed, generated: pathParentDerived, reason: "zone_id is the parent zone's server-assigned Z-ID (flag F5); the survey keeps client-named, the classifier calls the composition parent-derived", cohort: cohortParentComponent, tracking: "permanent"},
}

// The raw-signal headline figures, both sides pinned so drift in either
// file is a failure that names the numbers.
//
// SURVEY.md's "Raw signals" section originally hand-recorded 49/61/64;
// the file's reconstruction footnote keeps that record. Since the headline
// sentence became a rendered span (`go run ./tools/survey-gen -render`,
// increment 3), it carries the committed roster's figures read off the
// schemas in process - 47 taggable, 58 with native list resources, 61 with
// identity schemas - so the two pins below are now the same numbers on
// purpose. TestSurveyMDRenderedSpans holds the span to the artifact
// byte-for-byte; these pins hold both files to the figures a human last
// reviewed, so a provider bump that moves them is a visible failure here
// too.
var (
	handHeadlineCounts = Counts{Types: 68, Taggable: 47, ListResource: 58, IdentitySchema: 61}
	generatedCounts    = Counts{Types: 68, Taggable: 47, ListResource: 58, IdentitySchema: 61}
)

// headlineRe matches SURVEY.md's raw-signals sentence.
var headlineRe = regexp.MustCompile(
	`On the (\d+) curated types: (\d+) are taggable, (\d+) have native list resources, (\d+)\s+have provider identity schemas`)

// TestSurveyJSONAgainstHandTable diffs the committed live/survey.json
// against SURVEY.md's hand-written per-type table: same roster, per-type
// path classification modulo the documented exception list, per-type
// identity-schema signal against the Source column's schema/docs tier, and
// the raw-signal headline figures. It reads two committed files and nothing
// else, so it is not gated.
func TestSurveyJSONAgainstHandTable(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}

	handRows, err := readRoster(filepath.Join(root, surveyMDRel))
	if err != nil {
		t.Fatalf("parsing %s's per-type table: %v", surveyMDRel, err)
	}

	data, err := os.ReadFile(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("reading %s (regenerate with `go run ./tools/survey-gen`): %v", surveyJSONRel, err)
	}
	var survey Survey
	if err := json.Unmarshal(data, &survey); err != nil {
		t.Fatalf("decoding %s: %v", surveyJSONRel, err)
	}
	genRows := map[string]Row{}
	for _, r := range survey.Types {
		genRows[r.Type] = r
	}

	// Same roster, both ways.
	if len(genRows) != len(handRows) {
		t.Errorf("%s has %d types, %s's table has %d", surveyJSONRel, len(genRows), surveyMDRel, len(handRows))
	}

	usedExceptions := map[string]bool{}
	for _, hand := range handRows {
		gen, ok := genRows[hand.Type]
		if !ok {
			t.Errorf("%s is in %s's table but not in %s", hand.Type, surveyMDRel, surveyJSONRel)
			continue
		}

		// The one raw signal the hand table records per row: the Source
		// column's schema/docs derivation tier says whether the provider
		// ships an identity schema for the type.
		if gen.Signals.IdentitySchema != hand.SchemaTier {
			t.Errorf("%s: %s's Source tier says identity schema %v, the provider's schemas say %v",
				hand.Type, surveyMDRel, hand.SchemaTier, gen.Signals.IdentitySchema)
		}

		exc, hasExc := pathExceptions[hand.Type]
		switch {
		case gen.Path == hand.Path:
			if hasExc {
				usedExceptions[hand.Type] = true // already reported here, not again as unused
				t.Errorf("%s: stale exception (hand and generated both say %q now); remove it", hand.Type, gen.Path)
			}
		case !hasExc:
			t.Errorf("%s: path disagreement with no documented exception: %s says %q, the classifier says %q (evidence: %s)",
				hand.Type, surveyMDRel, hand.Path, gen.Path, gen.Evidence)
		case exc.hand != hand.Path || exc.generated != gen.Path:
			usedExceptions[hand.Type] = true // already reported here, not again as unused
			t.Errorf("%s: exception says hand=%q generated=%q, but the files say hand=%q generated=%q; update the exception",
				hand.Type, exc.hand, exc.generated, hand.Path, gen.Path)
		default:
			usedExceptions[hand.Type] = true
			t.Logf("documented exception %s: hand %q, classifier %q - %s", hand.Type, hand.Path, gen.Path, exc.reason)
		}
	}
	handTypes := map[string]bool{}
	for _, h := range handRows {
		handTypes[h.Type] = true
	}
	for typeName := range pathExceptions {
		switch {
		case !handTypes[typeName]:
			t.Errorf("exception for %s, which is not in %s's table", typeName, surveyMDRel)
		case !usedExceptions[typeName]:
			t.Errorf("stale exception for %s: no disagreement uses it", typeName)
		}
	}

	// The headline figures, both sides.
	md, err := os.ReadFile(filepath.Join(root, surveyMDRel))
	if err != nil {
		t.Fatal(err)
	}
	m := headlineRe.FindSubmatch(md)
	if m == nil {
		t.Fatalf("%s no longer carries the raw-signals headline sentence this test pins", surveyMDRel)
	}
	handGot := Counts{
		Types:          atoiOrFail(t, m[1]),
		Taggable:       atoiOrFail(t, m[2]),
		ListResource:   atoiOrFail(t, m[3]),
		IdentitySchema: atoiOrFail(t, m[4]),
	}
	if handGot != handHeadlineCounts {
		t.Errorf("%s's raw-signals headline moved: now %+v, pinned %+v; re-derive the expected deltas", surveyMDRel, handGot, handHeadlineCounts)
	}
	if survey.Counts != generatedCounts {
		t.Errorf("%s's counts moved: now %+v, pinned %+v", surveyJSONRel, survey.Counts, generatedCounts)
	}

	// Counts must also be what the rows say, so the two halves of the file
	// cannot drift apart.
	var recount Counts
	for _, r := range survey.Types {
		recount.Types++
		if r.Signals.Taggable {
			recount.Taggable++
		}
		if r.Signals.ListResource {
			recount.ListResource++
		}
		if r.Signals.IdentitySchema {
			recount.IdentitySchema++
		}
	}
	if recount != survey.Counts {
		t.Errorf("%s's counts %+v do not match its own rows %+v", surveyJSONRel, survey.Counts, recount)
	}
}

func atoiOrFail(t *testing.T, b []byte) int {
	t.Helper()
	n, err := strconv.Atoi(string(b))
	if err != nil {
		t.Fatalf("parsing %q as a number: %v", b, err)
	}
	return n
}

// ---------------------------------------------------------------------------
// SURVEY.md naming its own gap (issue #37, increment 3)
// ---------------------------------------------------------------------------

// exceptionTableRe matches live/SURVEY.md's "What the classifier does not
// settle" cohort table, row labels verbatim and in the same order the
// cohortXxx constants above are documented in.
var exceptionTableRe = regexp.MustCompile(`(?s)` +
	`\| Name-prefix idiom \(Optional\+Computed identifying argument\) \| (\d+) \|.*?` +
	`\| Account-derived import identity, not yet wired \| (\d+) \|.*?` +
	`\| Docs tier \(no identity schema in v6\.58\.0\) \| (\d+) \|.*?` +
	`\| Fork-wiring wrinkle \(deliberate, permanent\) \| (\d+) \|.*?` +
	`\| Parent component the survey keeps client-named \| (\d+) \|.*?` +
	`\| \*\*Total\*\* \| \*\*(\d+)\*\* \|`)

// TestExceptionCohortCounts holds live/SURVEY.md's "What the classifier
// does not settle" table to pathExceptions's own cohort tally. This is the
// count assertion issue #37's increment 3 asks for: the doc names the
// opentofu#2854 gap's size, and this test fails the moment pathExceptions
// changes - a cohort's count moves, a cohort appears or disappears, the
// total moves - without the doc's table following it.
func TestExceptionCohortCounts(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	md, err := os.ReadFile(filepath.Join(root, surveyMDRel))
	if err != nil {
		t.Fatal(err)
	}
	m := exceptionTableRe.FindSubmatch(md)
	if m == nil {
		t.Fatalf("%s no longer carries the path-exception cohort table this test pins", surveyMDRel)
	}
	docCounts := map[string]int{
		cohortNamePrefix:      atoiOrFail(t, m[1]),
		cohortAccountDerived:  atoiOrFail(t, m[2]),
		cohortDocsTier:        atoiOrFail(t, m[3]),
		cohortForkWrinkle:     atoiOrFail(t, m[4]),
		cohortParentComponent: atoiOrFail(t, m[5]),
	}
	docTotal := atoiOrFail(t, m[6])

	gotCounts := map[string]int{}
	for typeName, exc := range pathExceptions {
		if exc.cohort == "" {
			t.Errorf("%s: pathException carries no cohort", typeName)
			continue
		}
		gotCounts[exc.cohort]++
	}

	sumWant := 0
	for cohort, want := range docCounts {
		sumWant += want
		if got := gotCounts[cohort]; got != want {
			t.Errorf("%s's cohort table says %q is %d, pathExceptions has %d", surveyMDRel, cohort, want, got)
		}
	}
	for cohort, got := range gotCounts {
		if _, ok := docCounts[cohort]; !ok {
			t.Errorf("pathExceptions has %d entries in cohort %q, which %s's table does not name", got, cohort, surveyMDRel)
		}
	}
	if sumWant != docTotal {
		t.Errorf("%s's cohort table sums to %d but its own Total row says %d", surveyMDRel, sumWant, docTotal)
	}
	if len(pathExceptions) != docTotal {
		t.Errorf("pathExceptions has %d entries, %s's Total row says %d", len(pathExceptions), surveyMDRel, docTotal)
	}
}

// ---------------------------------------------------------------------------
// Tying each exception to an issue (issue #37, increment 4)
// ---------------------------------------------------------------------------

// trackingRe matches a pathException.tracking value that names an issue:
// "choudoufu#" followed by digits. The other legal value is the literal
// "permanent", checked separately.
var trackingRe = regexp.MustCompile(`^choudoufu#\d+$`)

// TestExceptionTracking is chant's KNOWN_FAILURES ratchet applied to
// pathExceptions: every entry must carry either a tracking issue or an
// explicit "permanent" marker, so a reader can tell which exceptions are
// waiting on upstream (and should disappear on their own) from which are
// deliberate fork design (and will not). The other half of the ratchet -
// TestSurveyJSONAgainstHandTable failing on a stale exception once its
// disagreement stops happening, and on an undocumented one - is unchanged
// by this field; this test only checks the reference itself is well-formed.
func TestExceptionTracking(t *testing.T) {
	for typeName, exc := range pathExceptions {
		switch {
		case exc.tracking == "":
			t.Errorf("%s: pathException carries no tracking issue or %q marker", typeName, "permanent")
		case exc.tracking == "permanent":
		case trackingRe.MatchString(exc.tracking):
		default:
			t.Errorf("%s: tracking %q is neither %q nor a choudoufu#NN issue reference", typeName, exc.tracking, "permanent")
		}
	}
}
