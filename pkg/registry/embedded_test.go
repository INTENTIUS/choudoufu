// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package registry

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestEmbeddedArtifactsMatchLive is the drift gate on the embedded copies:
// An embed directive cannot reach live/ at the repo root, so the package carries
// byte-for-byte copies, and regenerating the live/ artifacts must fail here
// until the copies are refreshed (cp live/mapping.json live/registry.json
// pkg/registry/).
func TestEmbeddedArtifactsMatchLive(t *testing.T) {
	root := repoRoot(t)
	for _, pair := range []struct {
		embedded []byte
		live     string
	}{
		{embeddedMappingJSON, "live/mapping.json"},
		{embeddedRegistryJSON, "live/registry.json"},
	} {
		want, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(pair.live)))
		if err != nil {
			t.Fatalf("reading %s: %v", pair.live, err)
		}
		if !bytes.Equal(pair.embedded, want) {
			t.Errorf("the embedded copy of %s differs from the live artifact; re-copy it into pkg/registry/ (the artifacts were regenerated without refreshing the embedded copies)", pair.live)
		}
	}
}

// repoRoot is declared in roster_test.go.

// TestEmbeddedRosterAnswersForMultiplex pins the embedded roster to the
// production question that exposed the missing wiring (#124's media cohort):
// aws_medialive_multiplex has no native provider list resource, and its
// discovery depends on the Cloud Control fallback finding the mapped,
// listable CFN type through this roster.
func TestEmbeddedRosterAnswersForMultiplex(t *testing.T) {
	r, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	cfnType, ok := r.EnumerationSource("aws_medialive_multiplex")
	if !ok {
		t.Fatal("EnumerationSource(aws_medialive_multiplex) = not ok; the embedded roster no longer offers the Cloud Control fallback its wiring exists for")
	}
	if cfnType != "AWS::MediaLive::Multiplex" {
		t.Fatalf("EnumerationSource(aws_medialive_multiplex) = %q, want AWS::MediaLive::Multiplex", cfnType)
	}

	// aws_prometheus_workspace is the service-alias tier's pin: its mapping
	// row carries via "service-alias", which the roster silently dropped
	// until #124's aps cohort failed replan on exactly this type.
	cfnType, ok = r.EnumerationSource("aws_prometheus_workspace")
	if !ok {
		t.Fatal("EnumerationSource(aws_prometheus_workspace) = not ok; service-alias mapping rows are no longer accepted as mapped")
	}
	if cfnType != "AWS::APS::Workspace" {
		t.Fatalf("EnumerationSource(aws_prometheus_workspace) = %q, want AWS::APS::Workspace", cfnType)
	}
}

// TestEmbeddedAmbiguousReverseJoins pins the reverse join's ambiguity in the
// committed mapping (GitHub issue #2286): 19 CFN types map to more than one
// TF type through TFTypesForCFNType's rows, which is why an adoption file
// names the engine type for them. A change in the count means the mapping
// moved; update the number and TFTypesForCFNType's comment together.
func TestEmbeddedAmbiguousReverseJoins(t *testing.T) {
	r, err := Embedded()
	if err != nil {
		t.Fatal(err)
	}
	ambiguous := 0
	for cfnType := range r.tfTypeFor {
		if len(r.TFTypesForCFNType(cfnType)) > 1 {
			ambiguous++
		}
	}
	if ambiguous != 19 {
		t.Errorf("%d CFN types map to more than one TF type, want 19 (TFTypesForCFNType's comment says 19)", ambiguous)
	}
	got := r.TFTypesForCFNType("AWS::ElasticLoadBalancingV2::LoadBalancer")
	if want := []string{"aws_alb", "aws_lb"}; !equalStrings(got, want) {
		t.Errorf("TFTypesForCFNType(AWS::ElasticLoadBalancingV2::LoadBalancer) = %v, want %v", got, want)
	}
	if got := r.TFTypesForCFNType("AWS::FSx::FileSystem"); len(got) != 4 {
		t.Errorf("TFTypesForCFNType(AWS::FSx::FileSystem) = %v, want four types", got)
	}
}
