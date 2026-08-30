// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"strings"
	"testing"
)

// TestProtocolLiveAWSNeverValidOnEstateResult proves #440's structural
// separation is load-bearing, not merely documented: an EstateResult
// carrying Protocol == ProtocolLiveAWS must be rejected by the exact
// predicate TestArtifactAgreesWithManifest uses. Per HANDOFF.md's "prove
// your checks can fail" rule, this is written from what the separation
// promises (a live-aws row never counts as a valid emulator row), not from
// the implementation, and it fails on purpose if IsValidEstateProtocol is
// ever loosened to accept it.
func TestProtocolLiveAWSNeverValidOnEstateResult(t *testing.T) {
	if IsValidEstateProtocol(ProtocolLiveAWS) {
		t.Fatal("ProtocolLiveAWS must never be a valid EstateResult.Protocol value - a live-aws certification belongs in Artifact.LiveCert, never a row in Artifact.Estates (see LiveCertResult's doc comment, livecert.go)")
	}
	rogue := EstateResult{Name: "reference-ec2-vpc", Protocol: ProtocolLiveAWS}
	if IsValidEstateProtocol(rogue.Protocol) {
		t.Fatal("a rogue EstateResult carrying ProtocolLiveAWS was accepted - TestArtifactAgreesWithManifest would silently let a live-aws verdict into the emulator-driven manifest rows")
	}
	// The two protocols an EstateResult DOES legitimately carry must still
	// be accepted - this function is a narrow exclusion, not a blanket
	// refusal that would make the positive case above vacuous.
	for _, p := range []string{ProtocolGauntlet, ProtocolLegacy} {
		if !IsValidEstateProtocol(p) {
			t.Errorf("IsValidEstateProtocol(%q) = false, want true", p)
		}
	}
}

// TestRebuildNeverTouchesLiveCert is #440 blocker 3's core claim, proven
// directly: populating a.LiveCert with a passing certification must not
// change a single number Rebuild computes from a.Estates - not the per-set
// Estates/Clear counts, not any stage's Tally. If it did, a live-aws pass
// would be silently inflating the "N of 25 core estates clear" headline bar
// exactly the way the brief warns against.
func TestRebuildNeverTouchesLiveCert(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}

	baseline := &Artifact{}
	baseline.Rebuild(m, nil, "sha256:baseline", OracleVersions{})

	withLiveCert := &Artifact{
		LiveCert: []LiveCertResult{{
			Estate: "reference-ec2-vpc", Protocol: ProtocolLiveAWS, Target: "aws",
			Region: "us-east-1", CeilingUSD: 5,
			Stages: map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass},
			Clear:  true, Date: "2026-08-29T00:00:00Z",
		}},
	}
	withLiveCert.Rebuild(m, nil, "sha256:baseline", OracleVersions{})

	if len(withLiveCert.LiveCert) != 1 || withLiveCert.LiveCert[0].Estate != "reference-ec2-vpc" {
		t.Fatalf("Rebuild must never modify a.LiveCert; got %+v", withLiveCert.LiveCert)
	}
	for key := range SetLabels {
		b, w := baseline.Sets[key], withLiveCert.Sets[key]
		if b.Estates != w.Estates || b.Clear != w.Clear {
			t.Errorf("set %q: adding a passing LiveCert row changed Estates/Clear from %d/%d to %d/%d - a live-aws certification is counting toward the emulator-driven headline bar", key, b.Clear, b.Estates, w.Clear, w.Estates)
		}
		for id, bt := range b.Stages {
			wt := w.Stages[id]
			if bt != wt {
				t.Errorf("set %q stage %q: tally changed from %+v to %+v after adding a LiveCert row", key, id, bt, wt)
			}
		}
	}
}

// TestLiveCertClearScopedToFourStages: liveCertClear must key off exactly
// LiveCertScopeStages() (cold_deploy, migrate, test_plan, test_apply, per
// #440's brief), never HeadlineStages() - a live cert says nothing about
// day2_rename/day2_remove/day2_count/day2_replace/greenfield, so those
// stages being absent or failing in a LiveCertResult's Stages map must not
// affect Clear.
func TestLiveCertClearScopedToFourStages(t *testing.T) {
	allFour := map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass}
	if !liveCertClear(allFour) {
		t.Fatal("all four scoped stages passing should be clear")
	}
	for _, id := range LiveCertScopeStages() {
		cp := map[string]string{}
		for k, v := range allFour {
			cp[k] = v
		}
		cp[id] = VerdictFail
		if liveCertClear(cp) {
			t.Errorf("stage %q failing should make liveCertClear false", id)
		}
	}
	// A stage OUTSIDE the scope failing (or simply absent) must not affect
	// the verdict - proves the scope is exactly four stages, not
	// accidentally every stage in the registry.
	outside := map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass, "day2_rename": VerdictFail}
	if !liveCertClear(outside) {
		t.Fatal("a failing day2_rename must not affect a live cert's Clear - it is outside LiveCertScopeStages()")
	}
}

// TestRenderLiveCertSectionIsSeparate: the rendered progress page carries a
// distinct, clearly-labeled section for live-cert evidence, and adding it
// leaves the emulator-estate table (and everything feeding {{< gauntlet-bars >}})
// textually unaffected - a live-aws row must never be conflated with
// emulator rows "anywhere they both appear ... including the rendered
// progress page" (#440's own wording).
func TestRenderLiveCertSectionIsSeparate(t *testing.T) {
	root := testRoot(t)
	m, err := LoadManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	without := &Artifact{}
	without.Rebuild(m, nil, "sha256:test", OracleVersions{})
	withoutPage := renderProgressIndex(without)
	if strings.Contains(withoutPage, "Live-AWS certification") {
		t.Fatal("renderLiveCertSection must render nothing when a.LiveCert is empty")
	}

	with := &Artifact{LiveCert: []LiveCertResult{{
		Estate: "reference-ec2-vpc", Protocol: ProtocolLiveAWS, Target: "aws",
		Region: "us-east-1", CeilingUSD: 5, Clear: true, Date: "2026-08-29T00:00:00Z",
		Stages: map[string]string{"cold_deploy": VerdictPass, "migrate": VerdictPass, "test_plan": VerdictPass, "test_apply": VerdictPass},
	}}}
	with.Rebuild(m, nil, "sha256:test", OracleVersions{})
	withPage := renderProgressIndex(with)

	if !strings.Contains(withPage, "## Live-AWS certification") {
		t.Fatal("expected a distinct '## Live-AWS certification' section when a.LiveCert is non-empty")
	}
	if !strings.Contains(withPage, "never counted toward either of") {
		t.Fatal("the live-cert section must say in words that it does not count toward the two headline bars")
	}
	if !strings.Contains(withPage, "reference-ec2-vpc | aws | us-east-1") {
		t.Fatalf("live-cert row not rendered as expected; page:\n%s", withPage)
	}

	// Every OTHER line of the page (the estate table, the bars shortcode,
	// the stage table, the run-time section) must be byte-identical with
	// and without the live-cert section - the addition must be purely
	// additive, never editing existing rendered evidence.
	beforeSection := strings.Index(withPage, "## Live-AWS certification")
	afterSection := strings.Index(withPage, "To add an estate")
	if beforeSection < 0 || afterSection < 0 || afterSection < beforeSection {
		t.Fatalf("could not locate the live-cert section bounds in the rendered page")
	}
	stripped := withPage[:beforeSection] + withPage[afterSection:]
	if stripped != withoutPage {
		t.Fatal("rendering a.LiveCert changed content OUTSIDE its own section - the separation is not purely additive")
	}
}
