// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package registry

import "testing"

// TestTaggableKnownSeparatesAMissFromARecordedFalse is issue #168's guard,
// and the test IS the deliverable here: no CFN type the roster maps is
// missing from live/registry.json today, so the path this protects is
// unreachable in the committed tree and will stay unreachable until an
// artifact regeneration makes it reachable without warning.
//
// The two artifacts are regenerated from different upstreams at different
// times. A mapping row naming a CFN type a newer registry no longer carries
// is ordinary skew, and before this split it reported as "live/registry.json
// records X as untaggable" - the artifact quoted as the source of a claim it
// never made, and the type silently dropped from the sweep on the strength
// of it.
func TestTaggableKnownSeparatesAMissFromARecordedFalse(t *testing.T) {
	mapping := []byte(`{"rows":[
		{"tf_type":"aws_recorded_taggable","cfn_type":"AWS::Test::Taggable","via":"name"},
		{"tf_type":"aws_recorded_untaggable","cfn_type":"AWS::Test::Untaggable","via":"name"},
		{"tf_type":"aws_absent","cfn_type":"AWS::Test::Absent","via":"name"}
	]}`)
	registry := []byte(`{"types":[
		{"type_name":"AWS::Test::Taggable","tagging":{"taggable":true},"handlers":{"list":true}},
		{"type_name":"AWS::Test::Untaggable","tagging":{"taggable":false},"handlers":{"list":false}}
	]}`)

	r, err := Parse(mapping, registry)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cases := []struct {
		cfnType      string
		wantTaggable bool
		wantKnown    bool
		because      string
	}{
		{"AWS::Test::Taggable", true, true, "recorded true"},
		{"AWS::Test::Untaggable", false, true, "recorded false - the type genuinely carries no tags"},
		{"AWS::Test::Absent", false, false, "no row at all - the registry recorded nothing"},
	}
	for _, tc := range cases {
		taggable, known := r.TaggableKnown(tc.cfnType)
		if taggable != tc.wantTaggable || known != tc.wantKnown {
			t.Errorf("TaggableKnown(%s) = (%v, %v), want (%v, %v) - %s",
				tc.cfnType, taggable, known, tc.wantTaggable, tc.wantKnown, tc.because)
		}
		// The bare form must keep answering exactly as it did, so this split
		// is additive for every caller that only wants the verdict.
		if got := r.Taggable(tc.cfnType); got != tc.wantTaggable {
			t.Errorf("Taggable(%s) = %v, want %v", tc.cfnType, got, tc.wantTaggable)
		}
	}

	// Listable carries the same split.
	if _, known := r.ListableKnown("AWS::Test::Absent"); known {
		t.Error("ListableKnown reports a row for a CFN type the registry does not carry")
	}
	if _, known := r.ListableKnown("AWS::Test::Untaggable"); !known {
		t.Error("ListableKnown reports no row for a CFN type the registry does carry")
	}
}

// TestTaggingDeclaredSeparatesSilenceFromADenial is issue #1327's guard, one
// rung below #168's above. TaggableKnown asks whether live/registry.json has
// a ROW for a CFN type; this asks whether that row's tagging BLOCK came from
// the CloudFormation schema or is registry-gen's default for a schema that
// carried no "tagging" key at all.
//
// Before this, the two were indistinguishable: registry-gen writes a tagging
// block for every row, so a silent schema got the zero value and marshalled
// byte-identically to one stating "taggable": false outright. 216 of the
// artifact's 1,683 rows are that case.
//
// The fixture below is the decisive pair. All three of these CFN types read
// taggable=false and known=true; only TaggingDeclared tells the middle one
// (CloudFormation denied it) from the last one (CloudFormation said nothing).
func TestTaggingDeclaredSeparatesSilenceFromADenial(t *testing.T) {
	mapping := []byte(`{"rows":[
		{"tf_type":"aws_declared_taggable","cfn_type":"AWS::Test::DeclaredTaggable","via":"name"},
		{"tf_type":"aws_declared_denied","cfn_type":"AWS::Test::DeclaredDenied","via":"name"},
		{"tf_type":"aws_silent","cfn_type":"AWS::Test::Silent","via":"name"},
		{"tf_type":"aws_absent","cfn_type":"AWS::Test::Absent","via":"name"}
	]}`)
	// AWS::Test::Silent has no "tagging" key at all, exactly as the 216
	// silent CloudFormation schemas do not.
	registryJSON := []byte(`{"types":[
		{"type_name":"AWS::Test::DeclaredTaggable","tagging":{"declared":true,"taggable":true},"handlers":{"list":true}},
		{"type_name":"AWS::Test::DeclaredDenied","tagging":{"declared":true,"taggable":false},"handlers":{"list":true}},
		{"type_name":"AWS::Test::Silent","handlers":{"list":true}}
	]}`)

	r, err := Parse(mapping, registryJSON)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	cases := []struct {
		cfnType      string
		wantDeclared bool
		wantKnown    bool
		wantTaggable bool
		because      string
	}{
		{"AWS::Test::DeclaredTaggable", true, true, true, "the schema declared a tagging block and said yes"},
		{"AWS::Test::DeclaredDenied", true, true, false, "the schema declared a tagging block and said no"},
		{"AWS::Test::Silent", false, true, false, "the schema said nothing; the false is registry-gen's default"},
		{"AWS::Test::Absent", false, false, false, "no row at all"},
	}
	for _, tc := range cases {
		declared, known := r.TaggingDeclared(tc.cfnType)
		if declared != tc.wantDeclared || known != tc.wantKnown {
			t.Errorf("TaggingDeclared(%s) = (%v, %v), want (%v, %v) - %s",
				tc.cfnType, declared, known, tc.wantDeclared, tc.wantKnown, tc.because)
		}
		// Additive: every existing caller must read exactly what it read
		// before. Moving one of these is a behaviour change with its own
		// argument to make, not a side effect of this split.
		if got := r.Taggable(tc.cfnType); got != tc.wantTaggable {
			t.Errorf("Taggable(%s) = %v, want %v - %s", tc.cfnType, got, tc.wantTaggable, tc.because)
		}
	}

	// The whole point, stated as the thing that must not be true: the denial
	// and the silence must not be the same answer.
	deniedDeclared, _ := r.TaggingDeclared("AWS::Test::DeclaredDenied")
	silentDeclared, _ := r.TaggingDeclared("AWS::Test::Silent")
	if deniedDeclared == silentDeclared {
		t.Errorf("a CFN type that denies tagging and one that says nothing about it both report declared=%v, "+
			"so the roster still conflates CloudFormation's silence with CloudFormation's denial", deniedDeclared)
	}
}

// TestEmbeddedArtifactRecordsTheSilentPopulation holds the split against the
// artifact that actually ships, not a fixture, and pins the population.
//
// 216 is derived from the pinned CloudFormation bundle and cross-checked two
// ways in tools/registry-gen: referenceCounts pins counts.tagging_undeclared,
// and TestRegistryCounts_MatchIssue42ReferenceValues re-tallies the rows
// against it. Those tests skip without the cached ~3MB bundle; this one needs
// nothing but the embedded artifact, so it runs everywhere.
//
// The two named types are the pair PR #1326 read out of the bundle by hand:
// AWS::IAM::Policy carries no tagging key, AWS::EC2::LaunchTemplate states
// "taggable": false. Both are admitted types that reach the sweep's
// registry-untaggable arm, and the arm's sentence attributes a
// CloudFormation denial to both.
func TestEmbeddedArtifactRecordsTheSilentPopulation(t *testing.T) {
	r, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}

	var silent, denied, taggable int
	for cfnType := range r.taggable {
		declared, _ := r.TaggingDeclared(cfnType)
		switch {
		case !declared:
			silent++
		case r.Taggable(cfnType):
			taggable++
		default:
			denied++
		}
	}
	if silent != 216 {
		t.Errorf("the embedded live/registry.json has %d rows whose CloudFormation schema said nothing about tagging, want 216 "+
			"(regenerate with `go run ./tools/registry-gen`, re-copy into internal/live/registry/, and move this figure only with a reviewed pin bump)", silent)
	}
	if denied != 432 {
		t.Errorf("the embedded live/registry.json has %d rows with an explicit \"taggable\": false, want 432", denied)
	}
	if taggable != 1035 {
		t.Errorf("the embedded live/registry.json has %d taggable rows, want 1035", taggable)
	}
	if got := silent + denied + taggable; got != len(r.taggable) {
		t.Errorf("the three buckets cover %d rows of %d", got, len(r.taggable))
	}

	policyDeclared, policyKnown := r.TaggingDeclared("AWS::IAM::Policy")
	if !policyKnown {
		t.Fatal("the embedded registry has no row for AWS::IAM::Policy")
	}
	if policyDeclared {
		t.Error("AWS::IAM::Policy's CloudFormation schema carries no tagging key, but the artifact records its tagging block as declared")
	}
	ltDeclared, ltKnown := r.TaggingDeclared("AWS::EC2::LaunchTemplate")
	if !ltKnown {
		t.Fatal("the embedded registry has no row for AWS::EC2::LaunchTemplate")
	}
	if !ltDeclared {
		t.Error("AWS::EC2::LaunchTemplate's CloudFormation schema states \"taggable\": false, but the artifact records its tagging block as undeclared")
	}
	if r.Taggable("AWS::IAM::Policy") || r.Taggable("AWS::EC2::LaunchTemplate") {
		t.Fatal("both types must still read untaggable: this change is additive and moves no type between the sweep's arms")
	}
}
