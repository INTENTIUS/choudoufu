// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"strings"
	"testing"
)

// TestCloudRegionComponentFromProviderBlock is issue #250's region half. A
// {Cloud: "region"} component is answered from the resource's own effective
// region - its `region` argument, or the region its resolved provider
// configuration declares - rather than from [Context.Cloud], which no
// production caller in this fork sets.
//
// Everything asserted here is a RENDERED identity, never a class or a
// predicate: this change turns refusals into resolutions, and the only
// thing that establishes a resolution is right is the string it builds.
func TestCloudRegionComponentFromProviderBlock(t *testing.T) {
	cfg := loadConfig(t, "testdata/provider-region-component", nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	// The one type in the table whose whole identity is the region. No
	// account, no cloud read, no CloudContext: plain Resolve settles it.
	here := resolutionAt(t, result, "aws_arczonalshift_autoshift_observer_notification_status.here")
	if here.Class != ClassConcrete {
		t.Errorf("arczonalshift status = %s, want CONCRETE - its identity is the provider block's region and nothing else", here.Class)
	}
	if here.ImportID != "eu-west-1" {
		t.Errorf("arczonalshift status import ID = %q, want %q", here.ImportID, "eu-west-1")
	}

	// A queue still needs the account, so it still defers - but on the
	// account now, not on the region. Before #250 the region was reported
	// as the missing value, because it is the first cloud component in the
	// row and nothing answered it.
	home := resolutionAt(t, result, "aws_sqs_queue.home")
	if home.Class != ClassNeedsDiscovery {
		t.Fatalf("aws_sqs_queue.home = %s with no account, want NEEDS_DISCOVERY", home.Class)
	}
	if home.ImportID != "" {
		t.Errorf("a needs-discovery resolution carries an import ID: %q", home.ImportID)
	}
	if !strings.Contains(home.Reason, "AWS account ID") {
		t.Errorf("the reason names the wrong missing value, want the account: %q", home.Reason)
	}
	if strings.Contains(home.Reason, "region") {
		t.Errorf("the reason still claims the region is unknown, but the provider block states it: %q", home.Reason)
	}
	if len(home.CauseArgs) != 1 || home.CauseArgs[0] != string(CloudAccountID) {
		t.Errorf("cause args = %v, want just %q", home.CauseArgs, CloudAccountID)
	}
}

// TestCloudRegionComponentIsPerResource is why the region is read per
// resource rather than out of one run-level field: two queues with the
// identical name, resolving to two different provider configurations in two
// different regions, are two different queues and must render two different
// URLs. A single [CloudContext.Region] would have named one region for both
// and produced one URL, which is a marker pointing at the wrong queue.
//
// The account here comes from [ResolveIn] purely so the URLs render in
// full; the region asserted is the provider block's, NOT the us-east-1 the
// context supplies, which is the point.
func TestCloudRegionComponentIsPerResource(t *testing.T) {
	cfg := loadConfig(t, "testdata/provider-region-component", nil)
	result, diags := ResolveIn(context.Background(), cfg, CloudContext{
		AccountID: "000000000000",
		Region:    "us-east-1",
	})
	assertNoErrors(t, diags)

	want := map[string]string{
		"aws_sqs_queue.home": "https://sqs.eu-west-1.amazonaws.com/000000000000/jobs",
		"aws_sqs_queue.away": "https://sqs.us-west-2.amazonaws.com/000000000000/jobs",
		"aws_arczonalshift_autoshift_observer_notification_status.here": "eu-west-1",
	}
	for addr, wantID := range want {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Errorf("%s = %s, want CONCRETE", addr, res.Class)
			continue
		}
		if res.ImportID != wantID {
			t.Errorf("%s import ID = %q, want %q", addr, res.ImportID, wantID)
		}
	}

	// The per-attribute rendering has to agree with the import ID, or an
	// import would ask the provider for a URL in a region the marker does
	// not name.
	home := resolutionAt(t, result, "aws_sqs_queue.home")
	if got := home.IdentityValues["url"]; got != want["aws_sqs_queue.home"] {
		t.Errorf(`aws_sqs_queue.home identity values["url"] = %q, want %q`, got, want["aws_sqs_queue.home"])
	}
}

// TestCloudRegionComponentStillCollides is the adversarial half: a region
// reaching the identity must not turn a duplicate into silence. "explicit"
// and "inherited" name the same queue in the same effective region by two
// different spellings, so they render the identical URL and must still be
// refused; "elsewhere" is the same name in another region and must not be.
//
// Run through [ResolveIn] because a collision is only checkable between two
// resolutions that actually rendered, and a queue needs the account to
// render at all.
func TestCloudRegionComponentStillCollides(t *testing.T) {
	cfg := loadConfig(t, "testdata/provider-region-collision", nil)
	result, diags := ResolveIn(context.Background(), cfg, CloudContext{AccountID: "000000000000"})

	explicit := resolutionAt(t, result, "aws_sqs_queue.explicit")
	inherited := resolutionAt(t, result, "aws_sqs_queue.inherited")
	elsewhere := resolutionAt(t, result, "aws_sqs_queue.elsewhere")
	if explicit.ImportID != inherited.ImportID {
		t.Fatalf("explicit=%q inherited=%q; the same queue by two spellings of the same region must render one URL", explicit.ImportID, inherited.ImportID)
	}
	if elsewhere.ImportID == explicit.ImportID {
		t.Fatalf("elsewhere renders %q, the same as explicit; it is in eu-west-2 and must not", elsewhere.ImportID)
	}

	var foundCollision, foundElsewhere bool
	for _, d := range diags {
		if d.Description().Summary != "Two resources with the same identity" {
			continue
		}
		detail := d.Description().Detail
		if strings.Contains(detail, "explicit") && strings.Contains(detail, "inherited") {
			foundCollision = true
		}
		if strings.Contains(detail, "elsewhere") {
			foundElsewhere = true
		}
	}
	if !foundCollision {
		t.Errorf("explicit and inherited render the identical URL and were not refused as a duplicate identity: %v", diags)
	}
	if foundElsewhere {
		t.Errorf("elsewhere was refused as a duplicate, but it is in a different region: %v", diags)
	}
}

// TestCloudRegionUnknownStillRefuses pins the floor. An estate whose
// provider block states no region - the AWS_REGION-from-the-environment
// spelling, which configuration alone cannot see - must refuse exactly as
// it did before #250, never render a URL with an empty region in it.
func TestCloudRegionUnknownStillRefuses(t *testing.T) {
	cfg := loadConfig(t, "testdata/cloud-scope-region-unknown", nil)

	var r resolver
	for _, cs := range []cloudScopeKey{
		{},
		{regionKnown: true, region: ""},
	} {
		if v, ok := r.cloudValueFor(CloudRegion, cs); ok {
			t.Errorf("cloudValueFor(region, %+v) = %q, true; want unknown", cs, v)
		}
	}
	// And an established region is answered, which is the whole change.
	if v, ok := r.cloudValueFor(CloudRegion, cloudScopeKey{regionKnown: true, region: "eu-west-1"}); !ok || v != "eu-west-1" {
		t.Errorf("cloudValueFor(region, eu-west-1) = %q, %v; want %q, true", v, ok, "eu-west-1")
	}
	// The account is never answered from a scope key, whatever it holds.
	if v, ok := r.cloudValueFor(CloudAccountID, cloudScopeKey{regionKnown: true, region: "eu-west-1"}); ok {
		t.Errorf("cloudValueFor(account-id, ...) = %q, true; the account has no configuration-only source", v)
	}

	// End to end: the fixture's own resources, whose type carries no cloud
	// component at all, are unaffected.
	result, diags := Resolve(context.Background(), cfg)
	_ = diags
	known := resolutionAt(t, result, "aws_cloudwatch_log_group.known")
	if known.Class != ClassConcrete {
		t.Errorf("aws_cloudwatch_log_group.known = %s, want CONCRETE", known.Class)
	}
}
