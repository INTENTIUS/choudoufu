// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/substrate"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestNoSweepLegFilesANamedGap is GitHub issue #1580's third condition: a
// family whose sweep no leg serves yields a sweep gap that names the family
// and the sweep it asked for, with the incomplete-sweep warning, and never
// a pass that listed nothing and says nothing.
func TestNoSweepLegFilesANamedGap(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	res, diags := discoverFixture(t, cloud, Request{
		Sweepers: []Sweeper{NoSweepLeg{Family: "graph", Kind: substrate.Sweep("graph-query")}},
	})
	assertNoErrors(t, diags)

	var gap *SweepGap
	for i := range res.SweepGaps {
		if res.SweepGaps[i].Reason == SweepGapNoSweepLeg {
			gap = &res.SweepGaps[i]
		}
	}
	if gap == nil {
		t.Fatalf("a family with no sweep leg filed no %s gap; gaps: %v", SweepGapNoSweepLeg, res.SweepGaps)
	}
	if gap.TypeName != "graph" || !strings.Contains(gap.Detail, `"graph-query"`) {
		t.Errorf("the gap does not name the family and its sweep: %s", gap)
	}
	var warned bool
	for _, d := range diags {
		if d.Severity() == tfdiags.Warning && d.Description().Summary == SummaryIncompleteSweep && strings.Contains(d.Description().Detail, "graph") {
			warned = true
		}
	}
	if !warned {
		t.Errorf("no %q warning names the family with no sweep leg; diagnostics: %v", SummaryIncompleteSweep, diags.ErrWithWarnings())
	}
}

// TestSweepersAreWhatRuns: the legs a request lists are the legs that run.
// Listing the AWS legs explicitly finds the deleted block exactly as the
// default does; a list without them finds nothing, which is the shape a
// caller that dropped a leg would see.
func TestSweepersAreWhatRuns(t *testing.T) {
	setup := func() *fakeCloud {
		cloud := newFakeCloud()
		ownWholeEstate(cloud)
		cloud.listable("aws_cloudwatch_log_group")
		cloud.own("aws_cloudwatch_log_group", "/estate/deleted", `aws_cloudwatch_log_group.deleted`)
		return cloud
	}

	res, diags := discoverFixture(t, setup(), Request{Sweep: true, Sweepers: []Sweeper{TaggingIndexSweep{}}})
	assertNoErrors(t, diags)
	if _, ok := removalsByAddr(res)[`aws_cloudwatch_log_group.deleted`]; !ok {
		t.Errorf("the listed tagging-index leg did not find the deleted block:\n%s", res)
	}

	res, diags = discoverFixture(t, setup(), Request{Sweep: true, Sweepers: []Sweeper{KubernetesSweep{}}})
	assertNoErrors(t, diags)
	if _, ok := removalsByAddr(res)[`aws_cloudwatch_log_group.deleted`]; ok {
		t.Errorf("a request that did not list the tagging-index leg ran it anyway:\n%s", res)
	}
}

// TestLegsNameTheirSweep: each leg serves the sweep its family names, which
// is what the caller keys the choice on.
func TestLegsNameTheirSweep(t *testing.T) {
	if got := (TaggingIndexSweep{}).Leg(); got != substrate.AWS.Sweep() {
		t.Errorf("TaggingIndexSweep serves %q, the AWS family asks for %q", got, substrate.AWS.Sweep())
	}
	if got := (KubernetesSweep{}).Leg(); got != substrate.Kubernetes.Sweep() {
		t.Errorf("KubernetesSweep serves %q, the Kubernetes family asks for %q", got, substrate.Kubernetes.Sweep())
	}
}

// TestEmptySweepersRunNoLeg (GitHub issue #1707): a request listing no
// legs runs none. Nil still means the one leg every caller before
// Request.Sweepers ran, but an empty list is a caller saying "nothing
// sweeps through this provider", and before this it quietly became the
// AWS legs: 1009 TYPE_NOT_LISTABLE gaps naming AWS types on a provider
// that serves none of them.
func TestEmptySweepersRunNoLeg(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, Sweepers: []Sweeper{}})
	assertNoErrors(t, diags)
	for _, g := range res.SweepGaps {
		if g.Reason == SweepGapNotListable {
			t.Fatalf("an empty leg list ran the tagging-index leg anyway: %s", g)
		}
	}
}

// TestUnclaimedNoSweepLegNamesTheProvider (GitHub issue #1707): the gap a
// provider no family claims files names the provider, and says no family
// claims it, rather than naming an empty sweep kind.
func TestUnclaimedNoSweepLegNamesTheProvider(t *testing.T) {
	cloud := newFakeCloud()
	ownWholeEstate(cloud)

	res, diags := discoverFixture(t, cloud, Request{Sweep: true, Sweepers: []Sweeper{NoSweepLeg{Family: "hashicorp/azurerm"}}})
	assertNoErrors(t, diags)
	var gap *SweepGap
	for i := range res.SweepGaps {
		if res.SweepGaps[i].Reason == SweepGapNoSweepLeg {
			gap = &res.SweepGaps[i]
		}
	}
	if gap == nil {
		t.Fatalf("no %s gap for an unclaimed provider; gaps: %v", SweepGapNoSweepLeg, res.SweepGaps)
	}
	if gap.TypeName != "hashicorp/azurerm" || !strings.Contains(gap.Detail, "No provider family claims provider hashicorp/azurerm") || strings.Contains(gap.Detail, `""`) {
		t.Errorf("the unclaimed provider's gap does not say what it is: %s", gap)
	}
}
