// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// Schema validation (issue #1051, item 5: "a test that the record's shape
// matches its schema, and one that fails when a field the schema requires
// is missing").
// ---------------------------------------------------------------------------

// TestScaleRecordSchemaValid proves the GREEN side: a record built the way
// BuildScaleRecordFromLiveCert actually builds one, from the exact detail
// text live/gauntlet.json carries today for terralith-scale's scale-50 real-
// AWS run (quoted verbatim - see TestBuildScaleRecordFromLiveCertScale50Fail
// below for where it comes from), validates clean and round-trips through
// JSON with every required field still present.
func TestScaleRecordSchemaValid(t *testing.T) {
	rec := BuildScaleRecordFromLiveCert(scale50LiveCertFixture(), "git show deadbeef:live/gauntlet.json")
	if err := ValidateScaleRecord(rec); err != nil {
		t.Fatalf("a record built from a real live_cert row failed validation: %v", err)
	}
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshalling a valid record: %v", err)
	}
	var round ScaleRecord
	if err := json.Unmarshal(b, &round); err != nil {
		t.Fatalf("unmarshalling a valid record's own JSON: %v", err)
	}
	if err := ValidateScaleRecord(round); err != nil {
		t.Fatalf("a record that round-tripped through JSON failed validation: %v\nJSON: %s", err, b)
	}
	if round.Estate != "terralith-scale" || round.Target != "aws" || round.Scale != 50 {
		t.Fatalf("round-tripped record lost identity fields: %+v", round)
	}
}

// TestValidateScaleRecordCatchesMissingField is the RED-then-GREEN proof
// item 5 asks for. It is written from what ValidateScaleRecord PROMISES -
// every one of Schema/Estate/Target/Commit/Source is required - not from
// reading the implementation, so tampering with any single one of them must
// turn a valid record invalid. See this worker's own report for the
// separate RED/GREEN run against a deliberately weakened
// ValidateScaleRecord (temporarily dropping the Estate check), quoted
// verbatim there per HANDOFF's "a check that cannot fail is not a check."
func TestValidateScaleRecordCatchesMissingField(t *testing.T) {
	valid := ScaleRecord{Schema: ScaleRecordSchema, Estate: "terralith-scale", Target: "aws", Commit: "abc123", Source: "test fixture"}
	if err := ValidateScaleRecord(valid); err != nil {
		t.Fatalf("a fully-populated minimal record should validate; got %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*ScaleRecord)
		want   string
	}{
		{"missing schema", func(r *ScaleRecord) { r.Schema = 0 }, "schema"},
		{"missing estate", func(r *ScaleRecord) { r.Estate = "" }, "estate"},
		{"missing target", func(r *ScaleRecord) { r.Target = "" }, "target"},
		{"missing commit", func(r *ScaleRecord) { r.Commit = "" }, "commit"},
		{"missing source", func(r *ScaleRecord) { r.Source = "" }, "source"},
		{"unknown target", func(r *ScaleRecord) { r.Target = "emulator" }, "target"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := valid
			c.mutate(&r)
			err := ValidateScaleRecord(r)
			if err == nil {
				t.Fatalf("%s: expected ValidateScaleRecord to reject this record, got nil error", c.name)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("%s: error %q does not mention %q", c.name, err.Error(), c.want)
			}
		})
	}

	// A stage carrying a verdict outside pass/fail/not_run must also be
	// caught - the same enum gauntlet_stage itself refuses to emit.
	withBadVerdict := valid
	withBadVerdict.Stages = map[string]ScaleStage{"cold_deploy": {Verdict: "maybe"}}
	if err := ValidateScaleRecord(withBadVerdict); err == nil {
		t.Fatal("a stage with verdict \"maybe\" should have failed validation")
	}
}

// ---------------------------------------------------------------------------
// Parsing: exact detail strings copied verbatim from live/gauntlet.json.
// ---------------------------------------------------------------------------

// scale50LiveCertFixture is terralith-scale's live_cert row exactly as it
// reads in live/gauntlet.json today (a real-AWS run at scale=50, 3,705
// resources, that did NOT clear - test_plan's post-migrate plan was not
// empty). Copied verbatim, not retyped from memory, so a change to this
// fixture is a change to the actual committed row.
func scale50LiveCertFixture() LiveCertResult {
	return LiveCertResult{
		Estate:     "terralith-scale",
		Protocol:   ProtocolLiveAWS,
		Target:     "aws",
		Region:     "us-east-2",
		CeilingUSD: 100,
		Stages: map[string]string{
			"cold_deploy": VerdictPass,
			"migrate":     VerdictPass,
			"test_plan":   VerdictFail,
		},
		Clear:    false,
		Commit:   "8bbef274d671b61342db452436abeb84820578a0",
		Date:     "2026-09-11T12:31:25Z",
		ExitCode: 1,
		Detail: map[string]string{
			"cold_deploy": "3705 resources from stock terraform against aws at scale=50 in 2023s, tofu-cert-run=lc1032s50b-run, debug log 70521448B/347 throttle/347 retry",
			"migrate":     "1655 of 3705 verified, 1655 stamped, 2050 skipped, in 1214s, debug log 94226106B/600 throttle/600 retry",
			"test_plan":   "the post-migrate plan is not empty - see /private/tmp/claude-501/-Users-alex-Documents-checkouts-intentius-choudoufu/aefed153-1ee1-4e68-9a5a-49f6e3e1cf34/scratchpad/scale50b/work/test_plan.out",
		},
		DurationS: 11180.5,
	}
}

// TestBuildScaleRecordFromLiveCertScale50Fail proves the whole prose-fallback
// path against the fixture above: resources/taggable/skipped/throttle/retry
// all recovered from cold_deploy and migrate's sentences even though the run
// FAILED at test_plan and that stage's own detail carries no numbers at all
// (a bare "the post-migrate plan is not empty" - no seconds, no throttle, no
// index_lag_s) - a failed stage still yields whatever the earlier stages
// measured, and a stage that measured nothing stays absent rather than
// reading zero.
func TestBuildScaleRecordFromLiveCertScale50Fail(t *testing.T) {
	rec := BuildScaleRecordFromLiveCert(scale50LiveCertFixture(), "fixture")

	if rec.Scale != 50 {
		t.Errorf("Scale = %d, want 50", rec.Scale)
	}
	if rec.Resources == nil {
		t.Fatal("Resources is nil, want a populated ScaleResources")
	}
	if rec.Resources.Total != 3705 {
		t.Errorf("Resources.Total = %d, want 3705", rec.Resources.Total)
	}
	if rec.Resources.Taggable != 1655 {
		t.Errorf("Resources.Taggable = %d, want 1655", rec.Resources.Taggable)
	}
	if rec.Resources.Skipped != 2050 {
		t.Errorf("Resources.Skipped = %d, want 2050", rec.Resources.Skipped)
	}

	cd := rec.Stages["cold_deploy"]
	// Seconds (this stage's own wall duration) is absent: this fixture's
	// LiveCertResult carries no Seconds map at all (it predates that field -
	// see LiveCertResult.Seconds's own doc comment), so BuildScaleRecordFromLiveCert
	// has nothing to read a real duration_s from. The inner-operation figure
	// the detail sentence DOES name ("... in 2023s") lands under
	// OperationSeconds instead, never silently standing in for Seconds - the
	// exact defect this unit fixes.
	if cd.Seconds != nil {
		t.Errorf("cold_deploy.Seconds = %v, want nil (this fixture's LiveCertResult carries no stage_seconds)", cd.Seconds)
	}
	if cd.OperationSeconds == nil || *cd.OperationSeconds != 2023 {
		t.Errorf("cold_deploy.OperationSeconds = %v, want 2023 (the inner apply's own reported time)", cd.OperationSeconds)
	}
	if cd.Throttle == nil || *cd.Throttle != 347 {
		t.Errorf("cold_deploy.Throttle = %v, want 347", cd.Throttle)
	}
	if cd.Retry == nil || *cd.Retry != 347 {
		t.Errorf("cold_deploy.Retry = %v, want 347", cd.Retry)
	}

	mg := rec.Stages["migrate"]
	if mg.Seconds != nil {
		t.Errorf("migrate.Seconds = %v, want nil, same reason as cold_deploy", mg.Seconds)
	}
	if mg.OperationSeconds == nil || *mg.OperationSeconds != 1214 {
		t.Errorf("migrate.OperationSeconds = %v, want 1214", mg.OperationSeconds)
	}
	if mg.Throttle == nil || *mg.Throttle != 600 {
		t.Errorf("migrate.Throttle = %v, want 600", mg.Throttle)
	}

	tp := rec.Stages["test_plan"]
	if tp.Verdict != VerdictFail {
		t.Errorf("test_plan.Verdict = %q, want fail", tp.Verdict)
	}
	if tp.Seconds != nil {
		t.Errorf("test_plan.Seconds = %v, want nil (no stage_seconds in the fixture)", tp.Seconds)
	}
	if tp.OperationSeconds != nil {
		t.Errorf("test_plan.OperationSeconds = %v, want nil (the fail detail names no duration)", tp.OperationSeconds)
	}
	if tp.Throttle != nil {
		t.Errorf("test_plan.Throttle = %v, want nil (the fail detail names no throttle count)", tp.Throttle)
	}
	if rec.IndexLagS != nil {
		t.Errorf("IndexLagS = %v, want nil (this row's test_plan detail carries no index_lag_s)", rec.IndexLagS)
	}
	if rec.PlanCalls != nil {
		t.Errorf("PlanCalls = %+v, want nil - no run this schema backfills measured the sweep/read-pass split", rec.PlanCalls)
	}
	// TotalSeconds/UnaccountedSeconds: the fixture's DurationS (11180.5) is
	// known, but no stage's Seconds is (all absent, as asserted above), so
	// the WHOLE total is unaccounted - honest, not a guess: this schema has
	// nothing else to attribute it to from what this fixture's LiveCertResult
	// carries. TestScalePatchSecondsReconciles below is where a source
	// outside this record's own JSON (the run's surviving work directory)
	// narrows that remainder.
	if rec.TotalSeconds == nil || *rec.TotalSeconds != 11180.5 {
		t.Fatalf("TotalSeconds = %v, want 11180.5", rec.TotalSeconds)
	}
	if rec.UnaccountedSeconds == nil || *rec.UnaccountedSeconds != 11180.5 {
		t.Errorf("UnaccountedSeconds = %v, want 11180.5 (no stage Seconds known at all)", rec.UnaccountedSeconds)
	}
	if err := ValidateScaleRecord(rec); err != nil {
		t.Fatalf("the built record failed validation: %v", err)
	}
}

// TestBuildScaleRecordFromLiveCertUsesRunnerDurationOverProse proves the
// fix's whole point: when the source LiveCertResult DOES carry Seconds (a
// run recorded after this field existed), a stage's own Seconds comes from
// there - the runner's actual duration_s - never from the detail sentence's
// inner-operation figure, even though both are present and different
// numbers (so a test that only checked ONE of them could not tell them
// apart). This also covers the failing stage: gauntlet_stage's delta-timer
// computes duration_s unconditionally, pass or fail, so a stage that never
// got as far as reporting its own "seconds=" token in its detail still gets
// a real Seconds value here.
func TestBuildScaleRecordFromLiveCertUsesRunnerDurationOverProse(t *testing.T) {
	r := scale50LiveCertFixture()
	// Deliberately different from the detail sentences' own "in 2023s" /
	// "in 1214s" figures, and present for test_plan (which failed and whose
	// detail names no duration at all) - proving the wall duration survives
	// independently of what the stage's own prose could report.
	r.Seconds = map[string]float64{"cold_deploy": 2035, "migrate": 2371, "test_plan": 1867}
	r.DurationS = 11180.5

	rec := BuildScaleRecordFromLiveCert(r, "fixture")

	cd := rec.Stages["cold_deploy"]
	if cd.Seconds == nil || *cd.Seconds != 2035 {
		t.Errorf("cold_deploy.Seconds = %v, want 2035 (from LiveCertResult.Seconds, not the detail's 2023s)", cd.Seconds)
	}
	if cd.OperationSeconds == nil || *cd.OperationSeconds != 2023 {
		t.Errorf("cold_deploy.OperationSeconds = %v, want 2023 (still kept, under its own name)", cd.OperationSeconds)
	}
	mg := rec.Stages["migrate"]
	if mg.Seconds == nil || *mg.Seconds != 2371 {
		t.Errorf("migrate.Seconds = %v, want 2371", mg.Seconds)
	}
	tp := rec.Stages["test_plan"]
	if tp.Verdict != VerdictFail {
		t.Fatalf("test_plan.Verdict = %q, want fail", tp.Verdict)
	}
	if tp.Seconds == nil || *tp.Seconds != 1867 {
		t.Errorf("test_plan.Seconds = %v, want 1867 - a FAILING stage must still carry its own wall duration (issue #1051/#1053's second defect)", tp.Seconds)
	}

	if rec.TotalSeconds == nil || *rec.TotalSeconds != 11180.5 {
		t.Fatalf("TotalSeconds = %v, want 11180.5", rec.TotalSeconds)
	}
	wantUnaccounted := 11180.5 - (2035 + 2371 + 1867)
	if rec.UnaccountedSeconds == nil || math.Abs(*rec.UnaccountedSeconds-wantUnaccounted) > 0.01 {
		t.Errorf("UnaccountedSeconds = %v, want %.1f (total minus the three known stage durations)", rec.UnaccountedSeconds, wantUnaccounted)
	}
	if err := ValidateScaleRecord(rec); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
}

// TestBuildScaleRecordFromLiveCertHistoricalScales exercises the same
// prose-fallback path against the three older real-AWS points recovered
// from live/gauntlet.json's own git history (scale 1/79, scale 4/301, scale
// 10/745 - see this worker's report for the exact commits) - detail text
// copied verbatim from `git show <rev>:live/gauntlet.json` at each one.
func TestBuildScaleRecordFromLiveCertHistoricalScales(t *testing.T) {
	cases := []struct {
		name             string
		r                LiveCertResult
		wantScale        int
		wantTotal        int
		wantTaggable     int
		wantSkipped      int
		wantColdSeconds  float64
		wantColdThrottle int
	}{
		{
			name: "scale1_79resources",
			r: LiveCertResult{
				Estate: "terralith-scale", Target: "aws", Commit: "da61fc08634970c2f63213029764277e2f9394ab",
				Stages: map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass},
				Detail: map[string]string{
					"cold_deploy": "79 resources from stock terraform against aws at scale=1 in 68s, tofu-cert-run=lc1788142850-14093, debug log 1426420B/4 throttle/4 retry",
					"migrate":     "38 of 79 verified, 38 stamped, 41 skipped, in 25s, debug log 2093233B/3 throttle/3 retry",
					"test_apply":  "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at 6",
					"test_plan":   "post-migrate plan is empty in 17s; zone/role tofu-address confirmed via the AWS CLI; debug log 758890 bytes, 0 throttling-error line(s), 0 retry line(s)",
				},
			},
			wantScale: 1, wantTotal: 79, wantTaggable: 38, wantSkipped: 41, wantColdSeconds: 68, wantColdThrottle: 4,
		},
		{
			name: "scale4_301resources",
			r: LiveCertResult{
				Estate: "terralith-scale", Target: "aws", Commit: "420d460c047132624e7a565fd8dc8803fd372ee2",
				Stages: map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass},
				Detail: map[string]string{
					"cold_deploy": "301 resources from stock terraform against aws at scale=4 in 174s, tofu-cert-run=lc1788117668-6931, debug log 5224563B/27 throttle/27 retry",
					"migrate":     "137 of 301 verified, 137 stamped, 164 skipped, in 77s, debug log 7772716B/38 throttle/38 retry",
					"test_apply":  "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at 12",
					"test_plan":   "post-migrate plan is empty in 267s; zone/role tofu-address confirmed via the AWS CLI; debug log 3279469 bytes, 3 throttling-error line(s), 3 retry line(s)",
				},
			},
			wantScale: 4, wantTotal: 301, wantTaggable: 137, wantSkipped: 164, wantColdSeconds: 174, wantColdThrottle: 27,
		},
		{
			name: "scale10_745resources",
			r: LiveCertResult{
				Estate: "terralith-scale", Target: "aws", Commit: "1d06e1d1774d4e38ff6836e56213e5a5272d44b8",
				Stages: map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass},
				Detail: map[string]string{
					"cold_deploy": "745 resources from stock terraform against aws at scale=10 in 413s, tofu-cert-run=lc1788143435-19631, debug log 13058408B/86 throttle/86 retry",
					"migrate":     "335 of 745 verified, 335 stamped, 410 skipped, in 222s, debug log 19403609B/190 throttle/190 retry",
					"test_apply":  "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at 24",
					"test_plan":   "post-migrate plan is empty in 129s; zone/role tofu-address confirmed via the AWS CLI; debug log 5744376 bytes, 2 throttling-error line(s), 2 retry line(s)",
				},
			},
			wantScale: 10, wantTotal: 745, wantTaggable: 335, wantSkipped: 410, wantColdSeconds: 413, wantColdThrottle: 86,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := BuildScaleRecordFromLiveCert(c.r, "fixture")
			if rec.Scale != c.wantScale {
				t.Errorf("Scale = %d, want %d", rec.Scale, c.wantScale)
			}
			if rec.Resources == nil {
				t.Fatal("Resources is nil")
			}
			if rec.Resources.Total != c.wantTotal {
				t.Errorf("Total = %d, want %d", rec.Resources.Total, c.wantTotal)
			}
			if rec.Resources.Taggable != c.wantTaggable {
				t.Errorf("Taggable = %d, want %d", rec.Resources.Taggable, c.wantTaggable)
			}
			if rec.Resources.Skipped != c.wantSkipped {
				t.Errorf("Skipped = %d, want %d", rec.Resources.Skipped, c.wantSkipped)
			}
			cd := rec.Stages["cold_deploy"]
			// None of these three fixtures' LiveCertResult carries a Seconds
			// map (all three predate that field), so cold_deploy's own wall
			// duration is genuinely unrecoverable here - it lands under
			// OperationSeconds (the detail sentence's own inner-operation
			// figure) instead, never silently standing in for Seconds.
			if cd.Seconds != nil {
				t.Errorf("cold_deploy.Seconds = %v, want nil (no stage_seconds on this fixture's LiveCertResult)", cd.Seconds)
			}
			if cd.OperationSeconds == nil || *cd.OperationSeconds != c.wantColdSeconds {
				t.Errorf("cold_deploy.OperationSeconds = %v, want %v", cd.OperationSeconds, c.wantColdSeconds)
			}
			if cd.Throttle == nil || *cd.Throttle != c.wantColdThrottle {
				t.Errorf("cold_deploy.Throttle = %v, want %v", cd.Throttle, c.wantColdThrottle)
			}
			if err := ValidateScaleRecord(rec); err != nil {
				t.Fatalf("validation failed: %v", err)
			}
		})
	}
}

// TestBuildScaleRecordFromEstateFloci exercises the OTHER shape - the
// `estates` array's own terralith-scale row (target=floci, scale=1), whose
// detail sentences carry no throttle instrumentation at all (floci never
// throttles). Text copied verbatim from live/gauntlet.json's own
// terralith-scale entry.
func TestBuildScaleRecordFromEstateFloci(t *testing.T) {
	e := EstateResult{
		Name: "terralith-scale",
		Stages: map[string]string{
			"cold_deploy": VerdictPass,
			"migrate":     VerdictPass,
			"test_plan":   VerdictPass,
			"test_apply":  VerdictPass,
		},
		LastRun: &LastRun{
			Commit:   "3bca740259d4373a831de32cb44c838ec38c1e51",
			Date:     "2026-09-10T07:49:05Z",
			Emulator: "ghcr.io/lex00/floci@sha256:9ec3fa649177f64c17c299e3fd799cc774cc1e1b67db2bc20e27d1bc98d7c264",
			Oracle:   &OracleVersions{Terraform: "1.15.8", Tofu: "1.12.5"},
			Detail: map[string]string{
				"cold_deploy": "stock terraform applied 79 resources at scale=1 from unmodified terralith-gen output into BOTH accounts (COLD keeps its terraform.tfstate for migrate to adopt from and is confirmed carrying no tofu-address tag; GREEN was enumerated at 34 objects, proved non-vacuous by a deliberately-added role, then destroyed back to an enumerated-empty account - issue #564's own proof, unchanged)",
				"migrate":     "live-import ratified 38 of 79 instances as eligible and stamped all 38 with 0 failed and 41 skipped (untaggable, identity composed from an already-stamped parent); every one of the 79 addresses in stock's own `terraform state list` - this stage's oracle - is accounted for by name in the report",
			},
			Seconds:   map[string]float64{"cold_deploy": 121, "migrate": 40, "test_plan": 3, "test_apply": 5},
			DurationS: 327.7,
		},
	}
	rec, ok := BuildScaleRecordFromEstate(e, "fixture")
	if !ok {
		t.Fatal("BuildScaleRecordFromEstate returned ok=false for a recognizable terralith-scale row")
	}
	if rec.Target != "floci" {
		t.Errorf("Target = %q, want floci", rec.Target)
	}
	if rec.Scale != 1 {
		t.Errorf("Scale = %d, want 1", rec.Scale)
	}
	if rec.Resources == nil || rec.Resources.Total != 79 {
		t.Fatalf("Resources = %+v, want Total=79", rec.Resources)
	}
	if rec.Resources.Taggable != 38 {
		t.Errorf("Taggable = %d, want 38", rec.Resources.Taggable)
	}
	if rec.Resources.Skipped != 41 {
		t.Errorf("Skipped = %d, want 41", rec.Resources.Skipped)
	}
	if got := rec.Stages["cold_deploy"].Seconds; got == nil || *got != 121 {
		t.Errorf("cold_deploy.Seconds = %v, want 121 (read from LastRun.Seconds, not parsed from prose)", got)
	}
	// This estate's four stages' own Seconds (121+40+3+5=169 - only these
	// four carry a verdict in the fixture) fall 158.7s short of the
	// 327.7s total, which UnaccountedSeconds must name rather than drop -
	// the real committed row's own fuller stage set (day2_count,
	// greenfield, etc.) closes to within 0.7s; this fixture only exercises
	// the four gauntlet-scope stages, so its own remainder is bigger and
	// that is expected.
	if rec.TotalSeconds == nil || *rec.TotalSeconds != 327.7 {
		t.Fatalf("TotalSeconds = %v, want 327.7", rec.TotalSeconds)
	}
	wantUnaccounted := 327.7 - (121 + 40 + 3 + 5)
	if rec.UnaccountedSeconds == nil || math.Abs(*rec.UnaccountedSeconds-wantUnaccounted) > 0.01 {
		t.Errorf("UnaccountedSeconds = %v, want %.1f", rec.UnaccountedSeconds, wantUnaccounted)
	}
	if err := ValidateScaleRecord(rec); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
}

// TestBuildScaleRecordFromEstateSkipsUnrecognized proves the negative case:
// an estate whose cold_deploy detail names no resource count at all (every
// module-example estate) yields ok=false, never a zero-valued record.
func TestBuildScaleRecordFromEstateSkipsUnrecognized(t *testing.T) {
	e := EstateResult{
		Name:   "corpus-ecs-fargate",
		Stages: map[string]string{"cold_deploy": VerdictPass},
		LastRun: &LastRun{
			Commit: "abc123",
			Detail: map[string]string{"cold_deploy": "applied the published module unmodified"},
		},
	}
	if _, ok := BuildScaleRecordFromEstate(e, "fixture"); ok {
		t.Fatal("expected ok=false for an estate with no recognizable resource count")
	}
}

// TestParseDetailPrefersTokensOverProse proves the token-first behaviour
// issue #1051 adds to terralith-scale.sh: when a detail string carries both
// the original prose AND explicit key=value tokens, the tokens win, so a
// future change to the prose wording can never silently change a number
// this schema already trusts.
func TestParseDetailPrefersTokensOverProse(t *testing.T) {
	// Deliberately WRONG prose numbers (999) beside CORRECT tokens (3705) -
	// if the parser ever preferred prose, this test would catch it by
	// reading the wrong value.
	detail := "999 resources from stock terraform against aws at scale=50 in 999s, debug log 1B/999 throttle/999 retry resources=3705 seconds=2023 throttle=347 retry=347"
	resources, scale, seconds, throttle, retry := parseColdDeployDetail(detail)
	if resources == nil || *resources != 3705 {
		t.Errorf("resources = %v, want 3705 (from the token, not the prose's 999)", resources)
	}
	if scale == nil || *scale != 50 {
		t.Errorf("scale = %v, want 50", scale)
	}
	if seconds == nil || *seconds != 2023 {
		t.Errorf("seconds = %v, want 2023 (from the token, not the prose's 999)", seconds)
	}
	if throttle == nil || *throttle != 347 {
		t.Errorf("throttle = %v, want 347 (from the token, not the prose's 999)", throttle)
	}
	if retry == nil || *retry != 347 {
		t.Errorf("retry = %v, want 347", retry)
	}
}

// TestUpsertScaleRecordKeepsEverySize proves ScaleArtifact's whole reason
// for existing over LiveCertResult's own row-per-estate storage: three
// records for the same estate/target at three different scales must all
// survive an Upsert sequence, and upserting the SAME scale again must
// replace only that one row.
func TestUpsertScaleRecordKeepsEverySize(t *testing.T) {
	var a ScaleArtifact
	a.UpsertScaleRecord(ScaleRecord{Schema: 1, Estate: "terralith-scale", Target: "aws", Scale: 1, Commit: "c1", Source: "s"})
	a.UpsertScaleRecord(ScaleRecord{Schema: 1, Estate: "terralith-scale", Target: "aws", Scale: 10, Commit: "c2", Source: "s"})
	a.UpsertScaleRecord(ScaleRecord{Schema: 1, Estate: "terralith-scale", Target: "aws", Scale: 50, Commit: "c3", Source: "s"})
	if len(a.Records) != 3 {
		t.Fatalf("len(Records) = %d, want 3 - a later scale must never overwrite an earlier one", len(a.Records))
	}
	a.UpsertScaleRecord(ScaleRecord{Schema: 1, Estate: "terralith-scale", Target: "aws", Scale: 10, Commit: "c2-rerun", Source: "s"})
	if len(a.Records) != 3 {
		t.Fatalf("len(Records) = %d, want 3 after re-upserting the same scale", len(a.Records))
	}
	for _, r := range a.Records {
		if r.Scale == 10 && r.Commit != "c2-rerun" {
			t.Errorf("scale 10's commit = %q, want the re-upserted c2-rerun", r.Commit)
		}
	}
}

// ---------------------------------------------------------------------------
// PlanCalls (issue #1053): the real-AWS token pair on test_plan's own
// detail. Item 5's second RED-then-GREEN proof - "one that fails when the
// parser drops a token it was given" - is this test, tampered and restored;
// see this worker's own report for the quoted RED output.
// ---------------------------------------------------------------------------

// TestParsePlanCallsDetailReadsBothTokens uses the exact numbers
// site/content/docs/model/plan-cost.md quotes for the scale-50 real-AWS run
// ("the first post-migration instrumented plan counted 8,305
// provider-mediated AWS API requests exactly ... against stock's 7,207 on
// the same run") to prove parsePlanCallsDetail reads BOTH sides off one
// detail string, not just the one it happens to be given first.
func TestParsePlanCallsDetailReadsBothTokens(t *testing.T) {
	detail := "post-migrate plan is empty in 375s; zone/role tofu-address confirmed via the AWS CLI; debug log 5744376 bytes, 0 throttling-error line(s), 0 retry line(s); index_lag_s=120 seconds=375 throttle=0 retry=0 plan_calls_choudoufu=8305 plan_calls_stock=7207"

	pc := parsePlanCallsDetail(detail)
	if pc == nil {
		t.Fatal("parsePlanCallsDetail returned nil for a detail string carrying both tokens")
	}
	if pc.Warm != nil {
		t.Errorf("Warm = %+v, want nil - terralith-scale.sh times only the first (cold) post-migrate plan, never a second", pc.Warm)
	}
	if pc.Cold == nil {
		t.Fatal("Cold is nil, want a populated pair")
	}
	if pc.Cold.Choudoufu != 8305 {
		t.Errorf("Cold.Choudoufu = %d, want 8305 (from plan_calls_choudoufu=8305)", pc.Cold.Choudoufu)
	}
	if pc.Cold.Stock == nil {
		t.Fatal("Cold.Stock is nil, want 7207 - the parser dropped the plan_calls_stock= token it was given")
	}
	if *pc.Cold.Stock != 7207 {
		t.Errorf("Cold.Stock = %d, want 7207 (from plan_calls_stock=7207)", *pc.Cold.Stock)
	}
}

// TestParsePlanCallsDetailAbsentToken proves the negative: no
// plan_calls_choudoufu= token at all (every historical row, and a run that
// predates this issue's instrumentation) yields nil, never a zero-valued
// ScalePlanCalls that would read as "measured, and it was zero".
func TestParsePlanCallsDetailAbsentToken(t *testing.T) {
	detail := "post-migrate plan is empty in 17s; zone/role tofu-address confirmed via the AWS CLI; debug log 758890 bytes, 0 throttling-error line(s), 0 retry line(s)"
	if pc := parsePlanCallsDetail(detail); pc != nil {
		t.Fatalf("parsePlanCallsDetail = %+v, want nil for a detail with no plan_calls_ token at all", pc)
	}
}

// TestBuildScaleRecordFromEstateKeepsOnlyStagesThisRunMeasured is the guard
// for the second half of issue #1069's own finding, discovered the moment a
// real scale-136 run met it.
//
// EstateResult.Stages is MERGED across runs by RunEstates (run.go): a stage
// this run never reached keeps the verdict an older run gave it, which is
// right for the board, where a stale verdict is still the best thing known
// about that stage. It is wrong for a ScaleRecord, whose whole identity is
// (estate, target, SCALE): a verdict measured at scale 1 is not a statement
// about scale 136, and copying it into a scale-136 record publishes a
// number nobody measured at that size.
//
// The real run this fixture copies is the one that found it. terralith-scale
// at SCALE=136 (10,069 resources) aborted at test_plan, so it spoke exactly
// three stages - cold_deploy, migrate, test_plan - while the row it wrote
// into carried eleven more verdicts from a scale-1 run eleven days older,
// including a test_apply "pass" that chant-bench scores. Published, that row
// would have said choudoufu's apply was verified at ten thousand resources
// when it was verified at seventy-nine.
//
// LastRun.Seconds is the witness, and is one only because #1069's own fix
// made it one: it now holds exactly the stages this run emitted a duration_s
// for, so its key set IS the set of stages this run measured. A row that
// recorded no per-stage seconds at all has no witness, and there the old
// carry-everything behaviour stands rather than silently emptying the record
// - see TestBuildScaleRecordFromEstateFloci, which exercises that path.
func TestBuildScaleRecordFromEstateKeepsOnlyStagesThisRunMeasured(t *testing.T) {
	e := EstateResult{
		Name: "terralith-scale",
		Stages: map[string]string{
			// The three this run actually spoke.
			"cold_deploy": VerdictPass,
			"migrate":     VerdictPass,
			"test_plan":   VerdictFail,
			// Eleven carried forward from an older, SMALLER run.
			"test_apply":       VerdictPass,
			"greenfield":       VerdictFail,
			"drift_reconverge": VerdictPass,
			"plan_approval":    VerdictPass,
			"day2_rename":      VerdictPass,
			"day2_remove":      VerdictPass,
			"day2_count":       VerdictPass,
			"day2_replace":     VerdictPass,
			"day2_crash":       VerdictNotRun,
			"day2_teardown":    VerdictNotRun,
			"strict":           VerdictNotRun,
		},
		LastRun: &LastRun{
			Commit:   "9525811174df01a74d374da31231102bbf598f5d",
			Date:     "2026-09-12T09:17:18Z",
			Emulator: "ghcr.io/lex00/floci@sha256:0bbeb43075c9df9c7e06311cd4eec99a354594d304faa4fe5899b494a009d23d",
			Detail: map[string]string{
				"cold_deploy": "stock terraform applied 10069 resources at scale=136 from unmodified terralith-gen output into BOTH accounts",
				"migrate":     "live-import ratified 4493 of 10069 instances as eligible and stamped all 4493",
				"test_plan":   "the post-migration plan exited 1, Rule: count-index - first: Error: count.index is not available in resource arguments",
				// Stale, from the scale-1 run, and must not ride along.
				"test_apply": "no-op apply (0 added, 0 changed, 0 destroyed); tofu-estate-tagged object count unchanged at 6",
			},
			Seconds:   map[string]float64{"cold_deploy": 8735, "migrate": 1398, "test_plan": 3},
			DurationS: 10136.2,
		},
	}
	rec, ok := BuildScaleRecordFromEstate(e, "fixture")
	if !ok {
		t.Fatal("BuildScaleRecordFromEstate returned ok=false for a recognizable terralith-scale row")
	}
	if rec.Scale != 136 {
		t.Errorf("Scale = %d, want 136", rec.Scale)
	}
	want := map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictFail}
	if len(rec.Stages) != len(want) {
		t.Errorf("record carries %d stage(s) %v, want exactly the %d this run measured %v - a verdict from another scale is not a measurement of this one",
			len(rec.Stages), stageVerdicts(rec), len(want), want)
	}
	for id, verdict := range want {
		if got, ok := rec.Stages[id]; !ok || got.Verdict != verdict {
			t.Errorf("stage %q = %+v, want verdict %q", id, got, verdict)
		}
	}
	for id := range rec.Stages {
		if _, measured := want[id]; !measured {
			t.Errorf("stage %q rode into the scale-136 record from an older run at a different scale", id)
		}
	}
	if _, carried := rec.Stages["test_apply"]; carried {
		t.Error("test_apply is the one chant-bench scores; carrying its scale-1 pass into a scale-136 record is exactly the published-number defect this guards")
	}
	// The arithmetic must still close, and now trivially does: the three
	// measured stages are the whole of the run.
	if rec.TotalSeconds == nil || *rec.TotalSeconds != 10136.2 {
		t.Fatalf("TotalSeconds = %v, want 10136.2", rec.TotalSeconds)
	}
	if rec.UnaccountedSeconds == nil || math.Abs(*rec.UnaccountedSeconds-0.2) > 0.01 {
		t.Errorf("UnaccountedSeconds = %v, want 0.2 (10136.2 - 8735 - 1398 - 3)", rec.UnaccountedSeconds)
	}
	if err := ValidateScaleRecord(rec); err != nil {
		t.Fatalf("validation failed: %v", err)
	}
}

// stageVerdicts renders a record's stage map for a failure message.
func stageVerdicts(r ScaleRecord) map[string]string {
	out := map[string]string{}
	for id, st := range r.Stages {
		out[id] = st.Verdict
	}
	return out
}
