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

// noladder_test.go: issue #1233's guard - an estate with no scale ladder can
// record a refusal, and a laddered estate still cannot record one without
// naming the rung it declined.
//
// The two rulings this composes are not negotiable and are each asserted
// here rather than assumed:
//
//   - #1151: a refusal is never written to live_cert. That array holds one
//     certification per estate and a refusal is the absence of one.
//   - #1231: a record that does not get written FAILS the run. A refusal is
//     the only record its run produces, so there is no second half to fall
//     back on.
//
// Before this unit the two composed into a dead end for reference-ec2-vpc:
// a $5 real-AWS certification of one fixed five-resource shape, with no rung
// anywhere. Its script's very first real step is
// `AMI="$(livecert_ami)" || fail "AMI resolution failed"`, and an account
// whose region resolves no Amazon Linux AMI is exactly a refusal - nothing
// was created, nothing is choudoufu's - yet `gauntlet_refused` from that
// estate failed the run with nothing the operator could do: passing a scale
// invents a rung nobody ran, not passing one fails. The library's own
// documented example, `gauntlet_refused - - - - "the account's AMI for this
// region is gone"`, is that same shape.

// noLadderRefusal is the record a refused reference-ec2-vpc run produces: a
// refusal with no scale, because the estate has no scale.
func noLadderRefusal() ScaleRecord {
	return ScaleRecord{
		Schema: ScaleRecordSchema, Estate: "reference-ec2-vpc", Target: "aws",
		Commit: "3db8029364aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Date: "2026-09-17T00:00:00Z",
		Outcome: ScaleOutcomeRefused,
		Refusal: &ScaleRefusal{Reason: "the account's Amazon Linux AMI for this region resolves to nothing, so nothing was created"},
		Source:  "gauntlet live-cert reference-ec2-vpc (commit 3db8029364)",
	}
}

// TestANonLadderEstateRecordsItsRefusalRatherThanFailingTheRun is the arm
// that matters: the refusal reaches a home, and it is not the ladder.
func TestANonLadderEstateRecordsItsRefusalRatherThanFailingTheRun(t *testing.T) {
	plan := planLiveCertScaleRow("reference-ec2-vpc", true, noLadderRefusal(), false)
	if plan.Err != nil {
		t.Fatalf("the refusal of an estate with no ladder failed the run, which is the dead end #1233 is about: %v", plan.Err)
	}
	if !plan.Write {
		t.Fatalf("the refusal was written nowhere and the run passed anyway - that is #1231's rule broken in the worst direction: note=%q", plan.Note)
	}
	if !plan.EstateLevel {
		t.Error("the refusal was routed to the ladder, where it would land at scale 0 - a rung nobody ran")
	}
}

// TestALadderedEstateStillFailsARefusalThatNamesNoRung is #1231 held. The
// new home must not become a dumping ground for the bug it was written to
// catch: terralith-scale forgetting `scale=` is a script bug, and shelving
// it estate-level would hide exactly which size was declined.
func TestALadderedEstateStillFailsARefusalThatNamesNoRung(t *testing.T) {
	rec := refusal136()
	rec.Scale = 0

	plan := planLiveCertScaleRow("terralith-scale", true, rec, true)
	if plan.Err == nil {
		t.Fatalf("a laddered estate's refusal with no scale was accepted (write=%v estateLevel=%v note=%q) - #1231's rule is that a record with no home fails the run", plan.Write, plan.EstateLevel, plan.Note)
	}
	if plan.Write {
		t.Error("it would have been written somewhere; there is no rung for it and the estate-level shelf is not one")
	}
	if !strings.Contains(plan.Err.Error(), "gauntlet_refused") {
		t.Errorf("the failure does not name the likely cause, so nobody can act on it: %v", plan.Err)
	}
	if !strings.Contains(plan.Err.Error(), "scale_ladder") {
		t.Errorf("the failure does not say what made this estate different from the one that records fine: %v", plan.Err)
	}

	// The same record, from an estate that declares no ladder, records.
	same := plan
	plan = planLiveCertScaleRow("terralith-scale", true, rec, false)
	if plan.Err != nil || !plan.EstateLevel {
		t.Errorf("the decision does not turn on the declaration at all: laddered=%+v unladdered=%+v", same, plan)
	}
}

// TestARefusalIsStillNeverACertification is #1151 held, for the shape that
// did not exist before: a refusal carrying no scale at all still leaves
// live_cert alone.
func TestARefusalIsStillNeverACertification(t *testing.T) {
	res := &ProtocolResult{
		Spoken:  true,
		Refusal: &ProtocolRefusal{Reason: "the account's Amazon Linux AMI for this region resolves to nothing"},
	}
	w := PlanLiveCertWrites("aws", res, RunStateFinished)
	if w.LiveCertRow {
		t.Errorf("a scale-less refusal would be written to %s's live_cert, replacing a certification with the absence of one (#1151)", ArtifactPath)
	}
	if !w.ScaleRecord {
		t.Errorf("a scale-less refusal records nothing at all, so the run's only evidence is its log: %s", w.Why)
	}
	if !strings.Contains(w.Why, "refusals") {
		t.Errorf("the note does not tell the operator where the refusal went: %q", w.Why)
	}
}

// TestAnEstateLevelRefusalNeverLandsOnTheLadder holds the two homes apart at
// the level that writes the file, not only at the level that decides. A
// refusal at scale 0 in `records` renders as the smallest rung.
func TestAnEstateLevelRefusalNeverLandsOnTheLadder(t *testing.T) {
	sa := &ScaleArtifact{Schema: ScaleRecordSchema, Records: []ScaleRecord{scale50()}}
	if _, err := sa.SupersedeScaleRecord(noLadderRefusal()); err == nil {
		t.Fatalf("the ladder accepted a refusal that names no scale; records now: %+v", sa.Records)
	} else if !strings.Contains(err.Error(), "absence of one") {
		t.Errorf("the refusal to write does not say why scale 0 is not a rung: %v", err)
	}
	if len(sa.Records) != 1 {
		t.Errorf("the ladder grew a row anyway: %d records", len(sa.Records))
	}

	// And the shelf holds nothing but scale-less refusals.
	if _, err := sa.SupersedeEstateRefusal(scale50()); err == nil {
		t.Error("the estate-level shelf accepted a measurement, which would read as a refusal")
	}
	if _, err := sa.SupersedeEstateRefusal(refusal136()); err == nil {
		t.Error("the estate-level shelf accepted a refusal that names scale 136 - that rung exists and the refusal belongs on it, beside whatever is measured there")
	}
	if len(sa.Refusals) != 0 {
		t.Errorf("a rejected write still landed: %+v", sa.Refusals)
	}
}

// TestTheNonLadderRefusalPathReachesDisk runs the whole path a refused
// reference-ec2-vpc run takes: the shell helper a script actually calls, the
// parser that reads its stdout, the record built from the run, the manifest
// declaration that says where it goes, the decision, and the write - then
// re-reads the file from disk.
//
// The parts before the record are the real ones rather than a hand-built
// ProtocolRefusal, because the whole gap was that the shell helper documents
// a scale-less refusal the Go side then had nowhere to put.
func TestTheNonLadderRefusalPathReachesDisk(t *testing.T) {
	repo := testRoot(t)
	lib := filepath.Join(repo, "live", "e2e", "lib", "gauntlet.sh")
	out, err := runBash(fmt.Sprintf(`set -uo pipefail
source %q
gauntlet_begin
gauntlet_refused - - - - "the account's Amazon Linux AMI for this region resolves to nothing (livecert_ami); nothing was created"
gauntlet_end
`, lib))
	if err != nil {
		t.Fatalf("gauntlet_refused with no scale failed: %v\n%s", err, out)
	}
	res, perr := ParseProtocol(strings.NewReader(string(out)))
	if perr != nil {
		t.Fatalf("the parser rejected a scale-less refusal the helper documents: %v\n%s", perr, out)
	}
	if res.Refusal == nil || res.Refusal.Scale != 0 {
		t.Fatalf("expected a refusal carrying no scale, got %+v", res.Refusal)
	}

	r := LiveCertResult{
		Estate: "reference-ec2-vpc", Protocol: ProtocolLiveAWS, Target: "aws",
		Region: "us-east-1", CeilingUSD: 5, ExitCode: 2,
		Commit: "3db8029364aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Date: "2026-09-17T00:00:00Z",
	}
	rec := BuildScaleRecordFromLiveCert(r, "gauntlet live-cert reference-ec2-vpc (commit 3db8029364)").WithRefusal(res.Refusal)
	if !rec.IsRefusal() || rec.Scale != 0 {
		t.Fatalf("the record built from the run is not a scale-less refusal: %+v", rec)
	}

	// The declaration comes from the committed manifest, the same way
	// cmdLiveCert reads it - so this test fails if reference-ec2-vpc is
	// ever declared laddered without the rest of this being revisited.
	m, err := LoadManifest(repo)
	if err != nil {
		t.Fatal(err)
	}
	laddered, err := EstateHasScaleLadder(m, "reference-ec2-vpc")
	if err != nil {
		t.Fatal(err)
	}
	if laddered {
		t.Fatal("reference-ec2-vpc now declares a scale ladder; this test and #1233's fix both assume it has none")
	}

	// A temp root carrying one measured ladder row, so the shelf write can
	// be shown not to disturb the ladder.
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := SaveScaleArtifact(root, &ScaleArtifact{Schema: ScaleRecordSchema, Records: []ScaleRecord{scale50()}}); err != nil {
		t.Fatal(err)
	}

	plan := planLiveCertScaleRow("reference-ec2-vpc", true, rec, laddered)
	if plan.Err != nil {
		t.Fatalf("the run failed with its refusal unrecorded: %v", plan.Err)
	}
	if !plan.Write {
		t.Fatalf("nothing was written and the run carried on - the refusal survives only in the log: %q", plan.Note)
	}
	written, err := saveLiveCertRecord(root, plan, rec)
	if err != nil {
		t.Fatalf("writing the refusal failed: %v", err)
	}
	if note := describeScaleWrite("reference-ec2-vpc", written); !strings.Contains(note, "ESTATE-LEVEL REFUSAL") || strings.Contains(note, "scale=0") {
		t.Errorf("what the runner prints reads as a rung: %q", note)
	}

	back, err := LoadScaleArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Refusals) != 1 {
		t.Fatalf("the refusal is not on disk: refusals=%+v", back.Refusals)
	}
	got := back.Refusals[0]
	if got.Estate != "reference-ec2-vpc" || got.Target != "aws" || got.Scale != 0 || !got.IsRefusal() {
		t.Errorf("the row on disk is not the estate-level refusal that was written: %+v", got)
	}
	if !strings.Contains(got.Refusal.Reason, "Amazon Linux AMI") {
		t.Errorf("the reason - the whole value of recording a refusal - did not survive: %q", got.Refusal.Reason)
	}
	if len(back.Records) != 1 || back.Records[0].Scale != 50 {
		t.Errorf("the ladder was disturbed by an estate-level write: %+v", back.Records)
	}

	// A second refused run supersedes the first and says what it replaced.
	again := rec
	again.Commit = "ffffffffffffffffffffffffffffffffffffffff"
	again.Date = "2026-09-18T00:00:00Z"
	again.Refusal = &ScaleRefusal{Reason: "still no Amazon Linux AMI in this region"}
	if _, err := saveLiveCertRecord(root, plan, again); err != nil {
		t.Fatalf("a second refusal for the same estate failed to record: %v", err)
	}
	back, err = LoadScaleArtifact(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Refusals) != 1 {
		t.Fatalf("the shelf grew a second row for the same (estate, target): %+v", back.Refusals)
	}
	if n := len(back.Refusals[0].Supersedes); n != 1 {
		t.Fatalf("supersedes chain is %d deep, want 1 - a replaced refusal must leave a pointer to what it replaced (#1151's second rule)", n)
	}
	if prev := back.Refusals[0].Supersedes[0]; prev.Commit != "3db8029364aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa" || prev.Outcome != ScaleOutcomeRefused {
		t.Errorf("the chain does not name the row it replaced: %+v", prev)
	}
}

// TestScaleLadderIsDeclaredByTheEstate: the property the decision turns on
// is a declaration, not a guess, and an estate nothing declares is an error
// rather than a default - both defaults lose a record or fail a recordable
// run.
func TestScaleLadderIsDeclaredByTheEstate(t *testing.T) {
	root := repoRootForTest(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]bool{"terralith-scale": true, "reference-ec2-vpc": false} {
		got, err := EstateHasScaleLadder(m, name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != want {
			t.Errorf("%s declares scale_ladder=%v, want %v", name, got, want)
		}
	}
	if _, err := EstateHasScaleLadder(m, "an-estate-nobody-declared"); err == nil {
		t.Error("an estate with no manifest entry was silently treated as unladdered, which shelves a laddered estate's scale-less refusal and loses the rung it declined")
	}
	if _, err := EstateHasScaleLadder(nil, "terralith-scale"); err == nil {
		t.Error("a nil manifest answered the question rather than saying it could not")
	}
}

// TestScaleLadderIsOmittedFromEveryUndeclaredEntry: adding the field changes
// no committed byte for the estates that do not use it - the same guard
// pre_apply has, for the same reason.
func TestScaleLadderIsOmittedFromEveryUndeclaredEntry(t *testing.T) {
	root := repoRootForTest(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	declared := 0
	for _, e := range m.Estates {
		if e.ScaleLadder {
			declared++
			continue
		}
		one := &Manifest{Estates: []Estate{e}}
		b, err := one.Canonical()
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(b), "scale_ladder") {
			t.Errorf("%s declares no scale ladder but its canonical entry carries the key:\n%s", e.Name, b)
		}
	}
	if declared == 0 {
		t.Error("no estate declares a scale ladder, so the laddered half of every test above is vacuous")
	}
}
