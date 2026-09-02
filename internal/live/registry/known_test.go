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
