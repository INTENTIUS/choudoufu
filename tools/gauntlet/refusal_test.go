// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// refusal_test.go: issue #1151's guard - a refused or aborted run must be
// recordable as its own outcome, and must not destroy the rung below it.
//
// The rung below it, at the time of writing, is terralith-scale/aws/scale 50:
// 3,705 resources against a real account, hours of paid runtime, and the
// ladder's high-water mark. Scale 136 is the refusal: 10,069 resources need
// 10,070 SSM parameters against a hard, non-adjustable 10,000 cap (#1146),
// with the uncapped alternative blocked behind #1145.

func TestParseProtocolReadsARefusalAndItsArithmetic(t *testing.T) {
	out := strings.Join([]string{
		"GAUNTLET protocol=1",
		"some ordinary log line",
		"GAUNTLET refused=1 scale=136 needed=10070 limit=10000 unit=ssm-parameters detail=10,069 resources need 10,070 SSM parameters against SSM's hard 10,000 cap (#1146)",
		"GAUNTLET end=1",
	}, "\n")
	res, err := ParseProtocol(strings.NewReader(out))
	if err != nil {
		t.Fatalf("ParseProtocol: %v", err)
	}
	if res.Refusal == nil {
		t.Fatal("no refusal parsed from a GAUNTLET refused=1 line")
	}
	if res.Refusal.Scale != 136 {
		t.Errorf("scale = %d, want 136", res.Refusal.Scale)
	}
	if res.Refusal.Needed == nil || *res.Refusal.Needed != 10070 || res.Refusal.Limit == nil || *res.Refusal.Limit != 10000 {
		t.Errorf("arithmetic = %v/%v, want 10070/10000", res.Refusal.Needed, res.Refusal.Limit)
	}
	if res.Refusal.Unit != "ssm-parameters" {
		t.Errorf("unit = %q", res.Refusal.Unit)
	}
	if !strings.Contains(res.Refusal.Reason, "#1146") {
		t.Errorf("the reason lost the rest of the line: %q", res.Refusal.Reason)
	}
	if !res.Spoken {
		t.Error("a refusing run still speaks the protocol")
	}
}

func TestParseProtocolRejectsAHalfSpokenRefusal(t *testing.T) {
	cases := map[string]string{
		"no reason at all":       "GAUNTLET protocol=1\nGAUNTLET refused=1 scale=136\n",
		"an empty reason":        "GAUNTLET protocol=1\nGAUNTLET refused=1 scale=136 detail=\n",
		"one side of the sum":    "GAUNTLET protocol=1\nGAUNTLET refused=1 needed=10070 unit=ssm detail=why\n",
		"numbers with no unit":   "GAUNTLET protocol=1\nGAUNTLET refused=1 needed=10070 limit=10000 detail=why\n",
		"a non-numeric scale":    "GAUNTLET protocol=1\nGAUNTLET refused=1 scale=big detail=why\n",
		"two different refusals": "GAUNTLET protocol=1\nGAUNTLET refused=1 detail=one\nGAUNTLET refused=1 detail=two\n",
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			if res, err := ParseProtocol(strings.NewReader(out)); err == nil {
				t.Errorf("accepted a refusal with %s: %+v - a half-spoken refusal recorded as a refusal is worse than no line", name, res.Refusal)
			}
		})
	}
}

// TestGauntletRefusedEmitsWhatParseProtocolReads is the seam: the shell
// helper a live-cert script actually calls, and the Go parser that reads its
// stdout, tested against each other rather than each against its own idea of
// the grammar.
func TestGauntletRefusedEmitsWhatParseProtocolReads(t *testing.T) {
	root := testRoot(t)
	lib := filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh")
	script := fmt.Sprintf(`set -uo pipefail
source %q
gauntlet_begin
gauntlet_refused 136 10070 10000 ssm-parameters "10,069 resources need 10,070 SSM parameters against SSM's hard 10,000 cap (#1146); the uncapped s3 store is blocked behind #1145"
gauntlet_end
`, lib)
	out, err := runBash(script)
	if err != nil {
		t.Fatalf("gauntlet_refused failed: %v\n%s", err, out)
	}
	res, perr := ParseProtocol(strings.NewReader(string(out)))
	if perr != nil {
		t.Fatalf("the Go parser rejected what the shell helper printed: %v\n%s", perr, out)
	}
	if res.Refusal == nil || res.Refusal.Scale != 136 || res.Refusal.Needed == nil || *res.Refusal.Needed != 10070 {
		t.Fatalf("the shell helper and the parser disagree: %+v\n%s", res.Refusal, out)
	}
	if !strings.Contains(res.Refusal.Reason, "#1145") {
		t.Errorf("the reason was truncated at the first space: %q", res.Refusal.Reason)
	}
}

// TestGauntletRefusedRefusesAHalfSpokenRefusal proves the shell helper's own
// guards red: each of these prints an error and exits 2 rather than emitting
// a line the record would then have to carry.
func TestGauntletRefusedRefusesAHalfSpokenRefusal(t *testing.T) {
	root := testRoot(t)
	lib := filepath.Join(root, "live", "e2e", "lib", "gauntlet.sh")
	cases := map[string]string{
		"no reason":            `gauntlet_refused 136 - - - ""`,
		"one side of the sum":  `gauntlet_refused 136 10070 - ssm-parameters "why"`,
		"numbers with no unit": `gauntlet_refused 136 10070 10000 - "why"`,
	}
	for name, call := range cases {
		t.Run(name, func(t *testing.T) {
			out, err := runBash(fmt.Sprintf("set -uo pipefail\nsource %q\n%s\n", lib, call))
			if err == nil {
				t.Errorf("gauntlet_refused accepted %s and printed:\n%s", name, out)
			}
			if strings.Contains(string(out), "GAUNTLET refused=1") {
				t.Errorf("gauntlet_refused emitted a protocol line for %s:\n%s", name, out)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// What a finished run writes
// ---------------------------------------------------------------------------

func TestPlanLiveCertWritesKeepsARefusalOutOfTheLiveCertRow(t *testing.T) {
	spoke := &ProtocolResult{Spoken: true, Stages: map[string]string{"cold_deploy": VerdictPass}}
	refused := &ProtocolResult{Spoken: true, Refusal: &ProtocolRefusal{Reason: "the cap", Scale: 136}}
	refusedMidRun := &ProtocolResult{
		Spoken:   true,
		Stages:   map[string]string{"cold_deploy": VerdictPass},
		Refusal:  &ProtocolRefusal{Reason: "the cap", Scale: 136},
		Detail:   map[string]string{},
		Unknown:  nil,
		PreApply: nil,
	}
	silent := &ProtocolResult{}

	if w := PlanLiveCertWrites("aws", spoke); !w.LiveCertRow || !w.ScaleRecord {
		t.Errorf("an ordinary measured run must write both rows, got %+v", w)
	}
	for name, res := range map[string]*ProtocolResult{"a refusal before any stage": refused, "a refusal after a stage passed": refusedMidRun} {
		w := PlanLiveCertWrites("aws", res)
		if w.LiveCertRow {
			t.Errorf("%s would have written the live_cert row - that row holds one certification per estate, so this replaces scale 50's (#1151)", name)
		}
		if !w.ScaleRecord {
			t.Errorf("%s wrote nothing at all; the refusal belongs on its own rung in %s", name, ScaleRecordsPath)
		}
		if !strings.Contains(w.Why, "#1151") {
			t.Errorf("%s: the explanation names no issue: %q", name, w.Why)
		}
	}
	if w := PlanLiveCertWrites("aws", silent); w.LiveCertRow || w.ScaleRecord {
		t.Errorf("a run that spoke nothing must write nothing (#1100), got %+v", w)
	}
	if w := PlanLiveCertWrites("floci", spoke); w.LiveCertRow || w.ScaleRecord {
		t.Errorf("a floci proving run must write nothing, got %+v", w)
	}
	if RecordsLiveCert(refused) {
		t.Error("RecordsLiveCert said yes to a refusal")
	}
}

// ---------------------------------------------------------------------------
// The record, and the superseding rule
// ---------------------------------------------------------------------------

func TestScaleOutcomeIsNotAPassJustBecauseNothingFailed(t *testing.T) {
	pass := map[string]ScaleStage{"cold_deploy": {Verdict: VerdictPass}}
	failed := map[string]ScaleStage{"cold_deploy": {Verdict: VerdictPass}, "test_plan": {Verdict: VerdictFail}}
	partial := map[string]ScaleStage{"cold_deploy": {Verdict: VerdictPass}, "migrate": {Verdict: VerdictPass}}

	if got := scaleOutcome(true, pass); got != ScaleOutcomePass {
		t.Errorf("a cleared run: got %q, want %q", got, ScaleOutcomePass)
	}
	if got := scaleOutcome(false, failed); got != ScaleOutcomeFail {
		t.Errorf("a run with a failed stage: got %q, want %q", got, ScaleOutcomeFail)
	}
	if got := scaleOutcome(false, partial); got != ScaleOutcomeNotRun {
		t.Errorf("a run that failed nothing but cleared nothing either: got %q, want %q - calling that a pass is the carried-verdict claim #1069 removed one level down", got, ScaleOutcomeNotRun)
	}
	if got := scaleOutcome(false, nil); got != ScaleOutcomeNotRun {
		t.Errorf("a run with no stages: got %q, want %q", got, ScaleOutcomeNotRun)
	}
}

func TestValidateScaleRecordHoldsOutcomeAndRefusalToEachOther(t *testing.T) {
	base := ScaleRecord{Schema: ScaleRecordSchema, Estate: "terralith-scale", Target: "aws", Scale: 136, Commit: "abc123", Source: "a test"}
	n, l := 10070, 10000

	good := base
	good.Outcome = ScaleOutcomeRefused
	good.Refusal = &ScaleRefusal{Reason: "SSM's hard cap (#1146)", Needed: &n, Limit: &l, Unit: "ssm-parameters"}
	if err := ValidateScaleRecord(good); err != nil {
		t.Fatalf("a well-formed refusal was rejected: %v", err)
	}

	bad := map[string]ScaleRecord{}
	r := base
	r.Outcome = ScaleOutcomeRefused
	bad["refused with no refusal attached"] = r
	r = base
	r.Outcome = ScaleOutcomePass
	r.Refusal = &ScaleRefusal{Reason: "the cap"}
	bad["a refusal attached to a pass"] = r
	r = base
	r.Outcome = ScaleOutcomeRefused
	r.Refusal = &ScaleRefusal{Reason: "   "}
	bad["a refusal with a blank reason"] = r
	r = base
	r.Outcome = ScaleOutcomeRefused
	r.Refusal = &ScaleRefusal{Reason: "the cap", Needed: &n, Unit: "ssm-parameters"}
	bad["one side of the arithmetic"] = r
	r = base
	r.Outcome = ScaleOutcomeRefused
	r.Refusal = &ScaleRefusal{Reason: "the cap", Needed: &n, Limit: &l}
	bad["arithmetic with no unit"] = r
	r = base
	r.Outcome = "aborted"
	bad["an outcome outside the four"] = r

	for name, rec := range bad {
		if err := ValidateScaleRecord(rec); err == nil {
			t.Errorf("ValidateScaleRecord accepted %s", name)
		}
	}

	// A row written before Outcome existed is still a valid row: absent
	// means "nobody recorded one", which is what every committed record
	// says today.
	if err := ValidateScaleRecord(base); err != nil {
		t.Errorf("a legacy row with no outcome was rejected: %v", err)
	}
}

// scale50 is the rung a refusal must not destroy - the shape of the row
// actually on record, with the numbers that cost the runtime.
func scale50() ScaleRecord {
	total := 11180.5
	return ScaleRecord{
		Schema: ScaleRecordSchema, Estate: "terralith-scale", Target: "aws", Scale: 50,
		Commit: "8bbef274d671b61342db452436abeb84820578a0", Date: "2026-09-11T12:31:25Z",
		Outcome:      ScaleOutcomeFail,
		Resources:    &ScaleResources{Total: 3705, Taggable: 1655, Skipped: 2050},
		Stages:       map[string]ScaleStage{"cold_deploy": {Verdict: VerdictPass}, "migrate": {Verdict: VerdictPass}, "test_plan": {Verdict: VerdictFail}},
		TotalSeconds: &total,
		Source:       "git show 27d062420e:live/gauntlet.json",
	}
}

func refusal136() ScaleRecord {
	n, l := 10070, 10000
	return ScaleRecord{
		Schema: ScaleRecordSchema, Estate: "terralith-scale", Target: "aws", Scale: 136,
		Commit: "3db8029364aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Date: "2026-09-17T00:00:00Z",
		Outcome: ScaleOutcomeRefused,
		Refusal: &ScaleRefusal{
			Reason: "10,069 resources need 10,070 SSM parameters against SSM's hard 10,000 cap (#1146); the uncapped s3 store is blocked behind #1145",
			Needed: &n, Limit: &l, Unit: "ssm-parameters",
		},
		Source: "gauntlet live-cert terralith-scale (commit 3db8029364)",
	}
}

func TestARefusalLandsOnItsOwnRungAndLeavesTheOneBelowAlone(t *testing.T) {
	sa := &ScaleArtifact{Schema: ScaleRecordSchema, Records: []ScaleRecord{scale50()}}
	before := scale50()

	written, err := sa.SupersedeScaleRecord(refusal136())
	if err != nil {
		t.Fatalf("recording a refusal at a scale nothing has measured was refused: %v", err)
	}
	if !written.IsRefusal() {
		t.Error("the row came back as something other than a refusal")
	}
	if len(sa.Records) != 2 {
		t.Fatalf("%d row(s) after recording the refusal, want 2", len(sa.Records))
	}
	var got50 *ScaleRecord
	for i := range sa.Records {
		if sa.Records[i].Scale == 50 {
			got50 = &sa.Records[i]
		}
	}
	if got50 == nil {
		t.Fatal("the scale-50 row is gone - a refusal at scale 136 destroyed the rung below it, which is the whole of #1151")
	}
	if got50.Commit != before.Commit || got50.Outcome != before.Outcome || got50.Resources.Total != 3705 {
		t.Errorf("the scale-50 row changed: %+v", *got50)
	}
	if len(got50.Supersedes) != 0 {
		t.Errorf("the scale-50 row grew a supersession from a write that never touched it: %+v", got50.Supersedes)
	}
}

func TestARefusalNeverReplacesAMeasurementAtTheSameScale(t *testing.T) {
	sa := &ScaleArtifact{Schema: ScaleRecordSchema, Records: []ScaleRecord{scale50()}}
	ref := refusal136()
	ref.Scale = 50 // the same rung, this time

	_, err := sa.SupersedeScaleRecord(ref)
	if err == nil {
		t.Fatal("a refusal replaced a measured row at the same scale; that row is hours of paid real-AWS runtime")
	}
	for _, want := range []string{"8bbef274d6", "#1151", "Nothing was written"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the refusal-to-write does not mention %q:\n%v", want, err)
		}
	}
	if len(sa.Records) != 1 || sa.Records[0].Outcome != ScaleOutcomeFail || sa.Records[0].Commit != scale50().Commit {
		t.Errorf("the artifact was modified by a write that reported an error: %+v", sa.Records)
	}
}

func TestSupersedingSaysWhatItReplaced(t *testing.T) {
	sa := &ScaleArtifact{Schema: ScaleRecordSchema, Records: []ScaleRecord{scale50()}}

	// The 2026-09-15 clear run of the same rung: newer wins.
	newer := scale50()
	newer.Commit = "3db8029364aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	newer.Date = "2026-09-15T00:00:00Z"
	newer.Outcome = ScaleOutcomePass
	newer.Stages = map[string]ScaleStage{"cold_deploy": {Verdict: VerdictPass}, "migrate": {Verdict: VerdictPass}, "test_plan": {Verdict: VerdictPass}, "test_apply": {Verdict: VerdictPass}}
	newer.Source = "gauntlet live-cert terralith-scale (commit 3db8029364)"

	written, err := sa.SupersedeScaleRecord(newer)
	if err != nil {
		t.Fatalf("a measurement could not supersede an older measurement: %v", err)
	}
	if len(sa.Records) != 1 {
		t.Fatalf("superseding appended instead of replacing: %d rows", len(sa.Records))
	}
	if len(written.Supersedes) != 1 {
		t.Fatalf("the new row records %d superseded row(s), want 1 - superseding silently is what #1151 is about", len(written.Supersedes))
	}
	sup := written.Supersedes[0]
	if sup.Commit != scale50().Commit || sup.Date != scale50().Date || sup.Outcome != ScaleOutcomeFail {
		t.Errorf("the supersession does not name the row it replaced: %+v", sup)
	}

	// And again: the chain keeps its history rather than forgetting the
	// middle of it.
	third := newer
	third.Commit = "cccccccccccccccccccccccccccccccccccccccc"
	third.Date = "2026-09-16T00:00:00Z"
	third.Supersedes = nil
	written, err = sa.SupersedeScaleRecord(third)
	if err != nil {
		t.Fatal(err)
	}
	if len(written.Supersedes) != 2 {
		t.Fatalf("the chain is %d deep after two supersessions, want 2: %+v", len(written.Supersedes), written.Supersedes)
	}
	if written.Supersedes[0].Commit != scale50().Commit || written.Supersedes[1].Commit != newer.Commit {
		t.Errorf("the chain is out of order or has lost a link: %+v", written.Supersedes)
	}
}

func TestSupersedingKeepsCallCountsTheNewRunCannotMeasure(t *testing.T) {
	old := scale50()
	old.PlanCalls = &ScalePlanCalls{}
	old.CallCountsSource = "the slicing bench at 9bd278a292"
	sa := &ScaleArtifact{Schema: ScaleRecordSchema, Records: []ScaleRecord{old}}

	newer := scale50()
	newer.Commit = "dddddddddddddddddddddddddddddddddddddddd"
	written, err := sa.SupersedeScaleRecord(newer)
	if err != nil {
		t.Fatal(err)
	}
	if written.PlanCalls == nil || written.CallCountsSource == "" {
		t.Errorf("superseding deleted call counts only scale-import-slice can produce, and the sentence saying where they came from: %+v", written)
	}
}

// TestARefusalSurvivesTheRoundTripToDisk is the file-level half: a refusal
// written to live/gauntlet-scale.json's own format, read back, and still a
// refusal with its arithmetic - with the rung below it byte-identical.
func TestARefusalSurvivesTheRoundTripToDisk(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	sa := &ScaleArtifact{Schema: ScaleRecordSchema, Records: []ScaleRecord{scale50()}}
	if err := SaveScaleArtifact(root, sa); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, ScaleRecordsPath))
	if err != nil {
		t.Fatal(err)
	}

	loaded, err := LoadScaleArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := loaded.SupersedeScaleRecord(refusal136()); err != nil {
		t.Fatal(err)
	}
	if err := SaveScaleArtifact(root, loaded); err != nil {
		t.Fatal(err)
	}

	back, err := LoadScaleArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	var ref, fifty *ScaleRecord
	for i := range back.Records {
		switch back.Records[i].Scale {
		case 136:
			ref = &back.Records[i]
		case 50:
			fifty = &back.Records[i]
		}
	}
	if ref == nil || !ref.IsRefusal() || ref.Refusal.Needed == nil || *ref.Refusal.Needed != 10070 || ref.Refusal.Unit != "ssm-parameters" {
		t.Fatalf("the refusal did not survive the round trip: %+v", ref)
	}
	if fifty == nil {
		t.Fatal("the scale-50 row did not survive the round trip")
	}
	// The scale-50 row's own bytes, unchanged: re-serializing it alone must
	// reproduce the file that existed before the refusal was recorded.
	only50 := &ScaleArtifact{Schema: back.Schema, Emulator: back.Emulator, Records: []ScaleRecord{*fifty}}
	if err := SaveScaleArtifact(root, only50); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(filepath.Join(root, ScaleRecordsPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the scale-50 row is not byte-identical after a refusal was recorded beside it:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// ---------------------------------------------------------------------------
// Where #1151 meets #1149: a record that does not get written fails the run
// ---------------------------------------------------------------------------

// TestARefusalWithNoScaleFailsTheRunRatherThanPrintingAndPassing is the
// judgement this unit had to make when #1149 landed on top of it.
//
// #1149's rule is that any reason the scale row does not get written is a
// failure of the run rather than an omission, because the two halves of a
// run's evidence must not disagree with nothing saying so. It kept exactly
// one exception: a certification that was never a scale measurement -
// reference-ec2-vpc - which has nothing to add and loses nothing by adding
// nothing.
//
// A refusal that names no scale is NOT that exception. A refusal is the only
// record its run produces, because PlanLiveCertWrites deliberately keeps it
// out of live_cert (#1151); with no scale there is no rung to put it on, and
// the run's entire result vanishes into a log. So: failure. The usual cause
// is a gauntlet_refused call that left out its scale, which is a bug in the
// script and should read as one.
//
// #1233 narrowed WHICH estates that applies to, without loosening it for
// this one: a refusal names no rung either because the script forgot to
// pass one or because the estate has no rungs, and only the estate's own
// `scale_ladder` declaration tells those apart. terralith-scale declares
// one, so the argument above is unchanged for it and this test still pins
// it. The estate that declares none records its refusal on a shelf of its
// own instead of failing - noladder_test.go.
func TestARefusalWithNoScaleFailsTheRunRatherThanPrintingAndPassing(t *testing.T) {
	refusedNoScale := refusal136()
	refusedNoScale.Scale = 0

	plan := planLiveCertScaleRow("terralith-scale", true, refusedNoScale, true)
	if plan.Err == nil {
		t.Fatalf("a refusal with no scale was treated as an omission (write=%v note=%q) - under #1149's rule a record that does not get written fails the run, and this record is the only one the run produced", plan.Write, plan.Note)
	}
	if plan.Write {
		t.Error("a refusal with no scale would have been written somewhere; there is no rung for it")
	}
	if !strings.Contains(plan.Err.Error(), "gauntlet_refused") {
		t.Errorf("the failure does not name the likely cause, so nobody can act on it: %v", plan.Err)
	}

	// The one legitimate omission is still an omission: a certification that
	// was never a scale measurement writes no row and does not fail.
	notAScaleRun := ScaleRecord{Schema: ScaleRecordSchema, Estate: "reference-ec2-vpc", Target: "aws", Commit: "abc123", Source: "a test"}
	plan = planLiveCertScaleRow("reference-ec2-vpc", true, notAScaleRun, false)
	if plan.Err != nil || plan.Write {
		t.Errorf("a certification that is not a scale measurement must be a quiet omission, got write=%v err=%v", plan.Write, plan.Err)
	}
	if !strings.Contains(plan.Note, "not a scale measurement") {
		t.Errorf("the omission does not say why it is one: %q", plan.Note)
	}

	// And an ordinary measured run is written.
	plan = planLiveCertScaleRow("terralith-scale", true, scale50(), true)
	if !plan.Write || plan.Err != nil {
		t.Errorf("a measured scale row was not written: write=%v err=%v note=%q", plan.Write, plan.Err, plan.Note)
	}
}

// TestSupersedeRefusalReachesTheCallerAsAnError is the other half of the
// same seam. SupersedeScaleRecord refusing to let a refusal replace a
// measurement (#1151) is a failure to write the run's record, so under
// #1149's rule it must reach cmdLiveCert's caller as an error - not be
// printed and swallowed, which is what a "we did not write it, carry on"
// path would do with the one thing this run had to say.
func TestSupersedeRefusalReachesTheCallerAsAnError(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	sa := &ScaleArtifact{Schema: ScaleRecordSchema, Records: []ScaleRecord{scale50()}}
	if err := SaveScaleArtifact(root, sa); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(root, ScaleRecordsPath))
	if err != nil {
		t.Fatal(err)
	}

	ref := refusal136()
	ref.Scale = 50 // the rung that is already measured
	written, err := saveLiveCertScaleRecord(root, ref)
	if err == nil {
		t.Fatalf("saveLiveCertScaleRecord accepted a refusal over a measured row and returned %+v", written)
	}
	if !strings.Contains(err.Error(), "refusing to replace a measured row") {
		t.Errorf("the error does not carry SupersedeScaleRecord's own words, so the caller cannot say what happened: %v", err)
	}

	after, err := os.ReadFile(filepath.Join(root, ScaleRecordsPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Errorf("the artifact changed on a write that reported an error:\nbefore:\n%s\nafter:\n%s", before, after)
	}
}

// TestDescribeScaleWriteNamesWhatItSuperseded holds the reporting half of
// #1151's second rule: superseding is allowed, and is never silent.
func TestDescribeScaleWriteNamesWhatItSuperseded(t *testing.T) {
	rec := scale50()
	rec.Commit = "3db8029364aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	rec.Supersedes = []ScaleSupersession{{Commit: "8bbef274d671b61342db452436abeb84820578a0", Date: "2026-09-11T12:31:25Z", Outcome: ScaleOutcomeFail}}
	got := describeScaleWrite("terralith-scale", rec)
	for _, want := range []string{"scale=50", "superseded", "8bbef274d6", "2026-09-11T12:31:25Z", "fail"} {
		if !strings.Contains(got, want) {
			t.Errorf("the write report does not mention %q:\n%s", want, got)
		}
	}

	ref := refusal136()
	got = describeScaleWrite("terralith-scale", ref)
	if !strings.Contains(got, "REFUSAL") || !strings.Contains(got, "#1146") {
		t.Errorf("a refusal's write report does not say it is a refusal, with the reason:\n%s", got)
	}
	if strings.Contains(got, "superseded") {
		t.Errorf("a row that replaced nothing claims to have superseded something:\n%s", got)
	}
}
