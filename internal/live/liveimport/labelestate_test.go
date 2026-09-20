// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// errorDiags is every error-severity diagnostic in diags, which is what
// "exactly one refusal" is counted over: a warning alongside it would be a
// different fact and must not pass for one of these.
func errorDiags(diags tfdiags.Diagnostics) []tfdiags.Diagnostic {
	var out []tfdiags.Diagnostic
	for _, d := range diags {
		if d.Severity() == tfdiags.Error {
			out = append(out, d)
		}
	}
	return out
}

// GitHub issue #1396's second half. An estate name is legal up to 128
// characters; a Kubernetes label value stops at 63. [approveLabel] and
// [approveManifest] each test that per object, so a migration of a
// Kubernetes estate under a 64-character name printed one identical FAILED
// line per object and the read-only run said nothing. Ratify refuses it
// once instead.
//
// Proving these red: delete the labelCarriers block at the end of [Ratify]
// and the three cases below that expect the refusal fail with "Ratify
// returned no error"; drop the labelCarriers > 0 guard from it and
// TestRatify_LongEstateIsFineForAnAWSOnlyState fails instead.

// tooLongEstate is a legal estate name one character past what a label
// value may be.
func tooLongEstate() string { return strings.Repeat("a", markers.LabelMaxValue+1) }

func TestRatify_LongEstateRefusedOnceForALabelSurfaceState(t *testing.T) {
	estate := tooLongEstate()
	if !markers.ValidEstateName(estate) {
		t.Fatalf("the test's own estate name %q is not a legal estate name; the case being tested is a LEGAL estate that is an illegal label", estate)
	}

	cloud := newLabelCloudProvider(map[string]string{"app": "web"})
	rat, diags := Ratify(context.Background(), Request{
		Estate:    estate,
		State:     configMapState(map[string]string{"app": "web"}),
		Providers: cloud,
	})

	errs := errorDiags(diags)
	if len(errs) != 1 {
		t.Fatalf("got %d error diagnostics, want exactly 1: %s", len(errs), diags.Err())
	}
	if rat != nil {
		t.Fatalf("Ratify returned a ratification alongside its refusal, so Approve can still be called and would report %d per-object outcome(s)", len(rat.Entries))
	}

	desc := errs[0].Description()
	for _, want := range []string{
		// The estate, named.
		estate,
		// Which rule it broke, and the limit that broke it.
		"64 characters long",
		"capped at 63",
		// How many objects it affects.
		"1 resource instance",
	} {
		if !strings.Contains(desc.Summary+" "+desc.Detail, want) {
			t.Errorf("the refusal does not contain %q:\n%s\n%s", want, desc.Summary, desc.Detail)
		}
	}

	if got, _ := markers.LabelsOf(cloud.object); got[markers.TagEstate] != "" {
		t.Error("Ratify wrote a label; it must never write")
	}
}

// The manifest surface is the other carrier the per-object check lives on,
// and it reaches Ratify by a different branch of ratifyOne, so it gets its
// own case rather than being assumed to follow.
func TestRatify_LongEstateRefusedOnceForAManifestSurfaceState(t *testing.T) {
	estate := tooLongEstate()
	cloud := newManifestCloudProvider()
	cluster := &fakeCluster{object: liveCronTab(nil)}

	rat, diags := Ratify(context.Background(), Request{
		Estate:    estate,
		State:     crontabState(),
		Providers: cloud,
		Clusters:  cluster,
	})

	errs := errorDiags(diags)
	if len(errs) != 1 {
		t.Fatalf("got %d error diagnostics, want exactly 1: %s", len(errs), diags.Err())
	}
	if rat != nil {
		t.Fatalf("Ratify returned a ratification alongside its refusal for a manifest-surface state; Approve would still report %d per-object outcome(s)", len(rat.Entries))
	}
	if len(cluster.patches) != 0 {
		t.Errorf("Ratify patched the cluster %d time(s); it must never write", len(cluster.patches))
	}
}

// The other direction, which is the one a mask wider than its label would
// break: the refusal is about the carrier, not about the estate name on its
// own. An AWS-only state under the same 64-character name has nothing that
// carries a label, so it ratifies exactly as it did before.
func TestRatify_LongEstateIsFineForAnAWSOnlyState(t *testing.T) {
	estate := tooLongEstate()
	cloud := newFakeCloud()

	rat, diags := Ratify(context.Background(), Request{
		Estate:    estate,
		State:     loadFixtureState(t),
		Providers: &fakeProviders{provider: cloud.provider()},
	})
	if diags.HasErrors() {
		t.Fatalf("an AWS-only state was refused for a name only Kubernetes objects cannot carry: %s", diags.Err())
	}
	if rat == nil {
		t.Fatal("Ratify returned no ratification for an AWS-only state")
	}
	if len(rat.Entries) == 0 {
		t.Fatal("Ratify returned no entries, so this case would pass on an empty state and prove nothing")
	}
}

// The count in the refusal is a count, not the word "1" with an "s"
// appended when there are more. Two objects, one line.
func TestRatify_LongEstateRefusalCountsTheObjects(t *testing.T) {
	estate := tooLongEstate()
	state := configMapState(nil)
	// A second ConfigMap in the same state, so the count has to move. Taken
	// off the first rather than written out again, so the two cannot drift
	// apart.
	first := state.RootModule().Resources["kubernetes_config_map.app"]
	if first == nil || first.Instances[addrs.NoKey] == nil {
		t.Fatal("the fixture no longer holds kubernetes_config_map.app")
	}
	second := addrs.Resource{Mode: addrs.ManagedResourceMode, Type: "kubernetes_config_map", Name: "app2"}.Instance(addrs.NoKey)
	state.RootModule().SetResourceInstanceCurrent(
		second, first.Instances[addrs.NoKey].Current, first.ProviderConfig, addrs.NoKey)

	rat, diags := Ratify(context.Background(), Request{
		Estate:    estate,
		State:     state,
		Providers: newLabelCloudProvider(nil),
	})
	errs := errorDiags(diags)
	if rat != nil || len(errs) != 1 {
		t.Fatalf("want exactly one refusal and no ratification, got rat=%v and %d errors", rat != nil, len(errs))
	}
	if detail := errs[0].Description().Detail; !strings.Contains(detail, "2 resource instances") {
		t.Errorf("the refusal does not count both objects:\n%s", detail)
	}
}

func TestLabelValueProblem_NamesTheClauseThatBroke(t *testing.T) {
	cases := map[string]struct {
		in   string
		want string
	}{
		"empty":           {in: "", want: "empty"},
		"too long":        {in: tooLongEstate(), want: "capped at 63"},
		"trailing hyphen": {in: "acme-", want: "label grammar"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := labelValueProblem(tc.in)
			if !strings.Contains(got, tc.want) {
				t.Errorf("labelValueProblem(%q) = %q, want it to mention %q", tc.in, got, tc.want)
			}
		})
	}
	if got := labelValueProblem("acme"); got != "" {
		t.Errorf("labelValueProblem on a legal label value = %q, want \"\"", got)
	}
}
