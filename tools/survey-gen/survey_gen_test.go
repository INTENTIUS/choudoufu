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

	schemas, err := acquireSchemas(defaultInitBin, t.TempDir(), testLogWriter{t})
	if err != nil {
		t.Fatalf("acquiring the provider schemas: %v", err)
	}

	got, err := buildSurvey(schemas, rosterTypes(roster)).marshal()
	if err != nil {
		t.Fatalf("marshaling the regenerated survey: %v", err)
	}
	want, err := os.ReadFile(filepath.Join(root, surveyJSONRel))
	if err != nil {
		t.Fatalf("reading the committed %s: %v", surveyJSONRel, err)
	}
	if bytes.Equal(got, want) {
		return
	}

	// Name the rows that moved rather than dumping two blobs.
	var gotS, wantS Survey
	if err := json.Unmarshal(got, &gotS); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(want, &wantS); err != nil {
		t.Fatalf("decoding the committed %s: %v", surveyJSONRel, err)
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
}

// pathExceptions is the full disagreement list between the hand table and
// the schema-derived classification, one documented entry per type. Five
// cohorts:
//
//   - name-prefix idiom: the type's identifying argument (name, bucket,
//     statement_id...) is Optional+Computed in the legacy-SDK schema
//     because a *_prefix alternative exists, and the strict client-named
//     rule deliberately stops at required arguments - accepting
//     Optional+Computed would admit aws_vpc by its id
//     (internal/live/identity/doc.go). The hand row knows the value
//     is client-chosen; the schema cannot prove it.
//   - account-derived import identity (SURVEY.md flags F1-F4): the
//     provider's required import attribute is an arn or url that embeds
//     account/region, so the schema says server-assigned while the survey
//     keeps the config-name judgment and flags the gap.
//   - docs tier: v6.58.0 ships no identity schema, so the hand row read
//     the provider's documented import grammar - a source the schema-only
//     classifier does not have.
//   - fork-wiring wrinkles SURVEY.md itself records (aws_eip) or
//     server-composed identities the four-path vocabulary cannot split
//     (aws_ecs_task_definition, aws_route_table_association,
//     aws_sns_topic_subscription).
//   - aws_route53_record, where the classifier and flag F5 agree that
//     zone_id is a parent's server-assigned Z-ID and the survey keeps
//     client-named anyway.
var pathExceptions = map[string]pathException{
	// --- name-prefix idiom (Optional+Computed identifying argument) ---
	"aws_cloudwatch_event_rule": {pathClientNamed, pathMarker, "name is Optional+Computed (name_prefix idiom); strict rule cannot prove client-naming, falls to marker"},
	"aws_cloudwatch_log_group":  {pathClientNamed, pathMarker, "name is Optional+Computed (name_prefix idiom); falls to marker"},
	"aws_db_subnet_group":       {pathClientNamed, pathMarker, "name is Optional+Computed (name_prefix idiom); falls to marker"},
	"aws_ecs_service":           {pathClientNamed, pathMarker, "cluster and name are Optional+Computed in the schema; falls to marker"},
	"aws_eks_node_group":        {pathClientNamed, pathMarker, "cluster_name/node_group_name are Optional+Computed (node_group_name_prefix idiom); falls to marker"},
	"aws_iam_instance_profile":  {pathClientNamed, pathMarker, "name is Optional+Computed (name_prefix idiom); falls to marker"},
	"aws_iam_role":              {pathClientNamed, pathMarker, "name is Optional+Computed (name_prefix idiom); falls to marker"},
	"aws_s3_bucket":             {pathClientNamed, pathMarker, "bucket is Optional+Computed (bucket_prefix idiom), the archetype in identity/doc.go; falls to marker"},
	"aws_autoscaling_group":     {pathClientNamed, pathListContent, "name is Optional+Computed (name_prefix idiom), and tags are tag blocks rather than a tags map, so the fallback is list+content"},
	"aws_iam_role_policy":       {pathClientNamed, pathListContent, "name is Optional+Computed (name_prefix idiom); untaggable, falls to list+content"},
	"aws_kms_alias":             {pathClientNamed, pathListContent, "name is Optional+Computed (name_prefix idiom); untaggable, falls to list+content"},
	"aws_lambda_permission":     {pathClientNamed, pathListContent, "statement_id is Optional+Computed (statement_id_prefix idiom); untaggable, falls to list+content"},

	// --- account-derived import identity (SURVEY.md flags F1, F3-F4) ---
	//
	// F2 (aws_sns_topic) is no longer here: the identity table builds the
	// topic's ARN out of the name plus the run's account and region, the
	// classifier reads that assertion, and both files say account-derived.
	// The three below are the ones the mechanism does not carry all the way
	// to a wired row, each for its own reason.
	"aws_sqs_queue":             {pathClientNamed, pathMarker, "required import attribute is the queue url (flag F1); the template is exact, but floci reports a queue's URL as its own endpoint and the provider's importer refuses it, so the marker path a context-less run takes cannot complete (choudoufu#26) and it is not wired"},
	"aws_iam_policy":            {pathClientNamed, pathMarker, "required import attribute is the policy arn (flag F3); the mechanism would build it, but floci's iam:GetPolicy omits Tags so the row cannot be proven live (choudoufu#26) and it is not wired"},
	"aws_secretsmanager_secret": {pathClientNamed, pathMarker, "required import attribute is the arn with a six-character server-generated suffix (flag F4), which no account/region template reconstructs; deferred to the marker path the classifier already reads off its taggability"},

	// --- docs tier: no identity schema in v6.58.0 ---
	"aws_ecs_cluster":                      {pathClientNamed, pathMarker, "no identity schema; hand row read the documented import grammar (name), classifier falls back to taggability"},
	"aws_key_pair":                         {pathClientNamed, pathMarker, "no identity schema; hand row read the documented import grammar (key_name), classifier falls back to taggability"},
	"aws_db_parameter_group":               {pathClientNamed, pathMarker, "no identity schema; hand row read the documented import grammar (name), classifier falls back to taggability"},
	"aws_iam_group":                        {pathClientNamed, pathOps, "no identity schema, untaggable and no native list resource; hand row read the documented import grammar (name)"},
	"aws_cloudfront_origin_access_control": {pathListContent, pathOps, "no identity schema and no native list resource in this release; the hand row kept the original pass's list+content claim"},

	// --- wrinkles SURVEY.md or c02d492 already records ---
	"aws_eip":                     {pathListContent, pathMarker, "SURVEY.md's own wrinkle: taggable, so the strongest-path rule says marker; the hand table shows the fork's list+content wiring"},
	"aws_ecs_task_definition":     {pathParentDerived, pathMarker, "identity is family plus a server-assigned revision; the classifier has no parent-derived-with-server-component shape and falls to marker"},
	"aws_route_table_association": {pathParentDerived, pathListContent, "the provider identifies an association by its rtbassoc- id (the c02d492 divergence); the hand row keeps the documented subnet+route-table import string"},
	"aws_sns_topic_subscription":  {pathParentDerived, pathListContent, "identity is the subscription arn, the parent topic arn plus a server uuid (flag F6); the schema sees only a server-assigned arn"},

	// --- parent component the survey flags but does not reclassify ---
	"aws_route53_record": {pathClientNamed, pathParentDerived, "zone_id is the parent zone's server-assigned Z-ID (flag F5); the survey keeps client-named, the classifier calls the composition parent-derived"},
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
