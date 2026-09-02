// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package foreign

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
)

// ---------------------------------------------------------------------------
// The removal section
// ---------------------------------------------------------------------------

// TestRemovalsAreReported: an orphan discovery marked for removal is printed
// in its own section, with the address it sits at in the prior state and the
// sentence that says why destroying it is legitimate.
func TestRemovalsAreReported(t *testing.T) {
	o := orphan("aws_cloudwatch_log_group", "/estate/gone", "", "aws_cloudwatch_log_group.gone")
	o.Addr, o.Addressable = discovery.UnescapeAddress(o.Normalized)
	o.Removal = true
	o.Swept = true

	res := classifyFixture(t, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{o}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_vpc", 1)}, SweepCovered: []string{"aws_cloudwatch_log_group"}}})

	if len(res.Removals) != 1 {
		t.Fatalf("want one removal, got:\n%s", res)
	}
	rm := res.Removals[0]
	if rm.Addr.String() != "aws_cloudwatch_log_group.gone" {
		t.Errorf("the removal sits at %q", rm.Addr)
	}
	if rm.LiveID != "/estate/gone" {
		t.Errorf("the removal does not carry the live ID: %q", rm.LiveID)
	}
	if !rm.BlockGone {
		t.Error("a removal whose resource block is absent is not marked BlockGone")
	}
	if !rm.Swept {
		t.Error("a removal found by the sweep is not marked Swept")
	}
	if !strings.Contains(rm.Why, "declares no aws_cloudwatch_log_group block") {
		t.Errorf("the removal's explanation does not say the block is gone: %q", rm.Why)
	}
	if len(res.SweepCovered) != 1 {
		t.Errorf("the sweep coverage was not carried through: %v", res.SweepCovered)
	}
}

// TestWithheldOrphansAreNotRemovals: an orphan discovery declined to plan a
// destroy for never appears as a removal, whatever the reason it was
// withheld. This pass reads that decision; it does not second-guess it.
func TestWithheldOrphansAreNotRemovals(t *testing.T) {
	o := orphan("aws_subnet", "subnet-xyz", "", "aws_subnet.this:a")
	o.Addr, o.Addressable = discovery.UnescapeAddress(o.Normalized)
	o.Withheld = "a declared instance of aws_subnet.this is unclaimed"

	res := classifyFixture(t, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{o}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_subnet", 1)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)}}})

	if len(res.Removals) != 0 {
		t.Errorf("a withheld orphan was reported as a removal:\n%s", res)
	}
	if len(res.Renames) != 1 {
		t.Errorf("the withheld orphan was not offered as a rename instead:\n%s", res)
	}
}

// TestRemovalOfALivingBlocksKey distinguishes the two sentences: a block that
// no longer expands to an instance key is not the same story as a block that
// is gone, even though the cloud call is identical.
func TestRemovalOfALivingBlocksKey(t *testing.T) {
	o := orphan("aws_subnet", "subnet-c", "", "aws_subnet.this:c")
	o.Addr, o.Addressable = discovery.UnescapeAddress(o.Normalized)
	o.Removal = true

	res := classifyFixture(t, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{o}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_subnet", 1)}}})

	if len(res.Removals) != 1 {
		t.Fatalf("want one removal, got:\n%s", res)
	}
	if res.Removals[0].BlockGone {
		t.Error("a removed instance key was reported as a deleted block")
	}
	if !strings.Contains(res.Removals[0].Why, "no longer expands") {
		t.Errorf("the explanation does not say the block still exists: %q", res.Removals[0].Why)
	}
}

// TestSweepGapsAreCarriedThrough: the holes in removal coverage travel with
// the report, because an empty removal list is only meaningful beside the
// list of types that were actually searched.
func TestSweepGapsAreCarriedThrough(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_vpc", 0)}, SweepGaps: []discovery.SweepGap{{
		TypeName: "aws_iam_role",
		Reason:   discovery.SweepGapNotListable,
		Detail:   "the provider cannot list it",
	}}}})

	if len(res.SweepGaps) != 1 {
		t.Fatalf("the sweep gap was dropped:\n%s", res)
	}
	if res.SweepGaps[0].TypeName != "aws_iam_role" || string(res.SweepGaps[0].Reason) != "TYPE_NOT_LISTABLE" {
		t.Errorf("the gap arrived as %+v", res.SweepGaps[0])
	}
}

// TestSweepRowsAreNotForeignCoverage: a sweep lists types the configuration
// does not declare, looking for markers. It never looked at unclaimed
// resources, so it must not appear in the foreign section's coverage - in
// either direction. Counting it as swept would claim knowledge of foreign
// resources nobody looked for; counting it as unswept would print a line
// about a type this configuration has nothing to do with.
func TestSweepRowsAreNotForeignCoverage(t *testing.T) {
	sweepScan := scan("aws_iam_role", 3)
	sweepScan.Sweep = true
	sweepScan.Scope = discovery.ScopeEstate
	sweepScan.Filtering = discovery.FilterServerSide

	res := classifyFixture(t, discovery.Result{Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_vpc", 1), sweepScan}}})

	for _, s := range res.Swept {
		if s == "aws_iam_role" {
			t.Errorf("a swept type was reported as covered by the foreign classification:\n%s", res)
		}
	}
	if u, ok := res.UnsweptOf("aws_iam_role"); ok && u.Reason != UnsweptNotScanned {
		t.Errorf("the sweep row changed the type's foreign coverage to %s:\n%s", u.Reason, res)
	}
}

// ---------------------------------------------------------------------------
// Content-strengthened rename pairing
// ---------------------------------------------------------------------------

// contentFixture is a configuration whose for_each block fixes one of the
// arguments the match table names, which is the only shape content can say
// anything about. A block whose arguments all come from each.value has
// nothing readable from configuration alone, and the pairing then rests
// entirely on the block match, exactly as it did before.
const contentFixture = `
terraform {
  required_providers {
    aws = {
      source = "hashicorp/aws"
    }
  }
}

locals {
  groups = toset(["a", "b", "c"])
}

resource "aws_security_group" "web" {
  for_each = local.groups

  name        = "fixed-name"
  description = "content fixture"

  tags = {
    tofu-estate  = "stateless-e2e"
    tofu-address = "aws_security_group.web:${each.key}"
  }
}
`

func contentDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(contentFixture), 0o600); err != nil {
		t.Fatalf("writing the content fixture: %v", err)
	}
	return dir
}

func classifyIn(t *testing.T, dir string, res discovery.Result) *Result {
	t.Helper()

	out, diags := Classify(context.Background(), Request{
		Estate: estateName,
		Config: loadConfig(t, dir),
		Report: &res.Report, Orphans: res.Orphans,
	})
	if diags.HasErrors() {
		t.Fatalf("classification failed:\n%s", renderDiags(diags))
	}
	return out
}

// withObject gives an orphan the live object a content comparison reads.
func withObject(o discovery.OwnedResource, attrs map[string]string) discovery.OwnedResource {
	vals := make(map[string]cty.Value, len(attrs))
	for k, v := range attrs {
		vals[k] = cty.StringVal(v)
	}
	o.Resource = cty.ObjectVal(vals)
	return o
}

// TestContentDoesNotCreateAPairing is the rule stated as a test: content is
// never the reason two things are paired. An orphan of one block and an
// unclaimed instance of another are not offered for each other however well
// their arguments agree.
func TestContentDoesNotCreateAPairing(t *testing.T) {
	dir := contentDir(t)

	// An orphan of a block the configuration does not have at all, carrying
	// exactly the arguments aws_security_group.web declares.
	o := withObject(orphan("aws_security_group", "sg-1", "", "aws_security_group.other:a"),
		map[string]string{"name": "fixed-name"})

	res := classifyIn(t, dir, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{o}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_security_group", 1)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_security_group.web["c"]`)}}})

	if len(res.Renames) != 0 {
		t.Errorf("content created a pairing across two resource blocks:\n%s", res)
	}
}

// TestContentDisqualifiesAPairing: the block match would have offered this
// rename, and content takes it away. The live resource's identifying argument
// is not the one the block declares, so it is not an instance of that block
// under a new key.
func TestContentDisqualifiesAPairing(t *testing.T) {
	dir := contentDir(t)

	o := withObject(orphan("aws_security_group", "sg-1", "", "aws_security_group.web:a"),
		map[string]string{"name": "some-other-name"})

	res := classifyIn(t, dir, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{o}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_security_group", 1)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_security_group.web["c"]`)}}})

	if len(res.Renames) != 0 {
		t.Fatalf("a rename was offered for a resource whose content disagrees:\n%s", res)
	}
	amb, ok := res.AmbiguousFor("aws_security_group.web")
	if !ok {
		t.Fatalf("the disqualified pairing was not reported at all:\n%s", res)
	}
	if !strings.Contains(amb.Detail, "some-other-name") || !strings.Contains(amb.Detail, "fixed-name") {
		t.Errorf("the explanation does not show the comparison: %q", amb.Detail)
	}
}

// TestContentAgreeingLeavesTheBlockMatchAlone: the ordinary case. Content
// agrees, and the pairing is the one the block match already made.
func TestContentAgreeingLeavesTheBlockMatchAlone(t *testing.T) {
	dir := contentDir(t)

	o := withObject(orphan("aws_security_group", "sg-1", "", "aws_security_group.web:a"),
		map[string]string{"name": "fixed-name"})

	res := classifyIn(t, dir, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{o}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_security_group", 1)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_security_group.web["c"]`)}}})

	if len(res.Renames) != 1 {
		t.Fatalf("the agreeing pairing was not offered:\n%s", res)
	}
	if got := res.Renames[0].Old.String(); got != `aws_security_group.web["a"]` {
		t.Errorf("the rename is from %q", got)
	}
	if len(res.Renames[0].MatchedOn) != 1 || res.Renames[0].MatchedOn[0].Attr != "name" {
		t.Errorf("the candidate does not record what content agreed on: %v", res.Renames[0].MatchedOn)
	}
}

// TestContentResolvesAnAmbiguity: two orphans and one unclaimed instance is
// ambiguous on the block match alone. Content rules one of them out entirely,
// and what is left is a single pairing with positive agreement behind it.
func TestContentResolvesAnAmbiguity(t *testing.T) {
	dir := contentDir(t)

	ours := withObject(orphan("aws_security_group", "sg-ours", "", "aws_security_group.web:a"),
		map[string]string{"name": "fixed-name"})
	theirs := withObject(orphan("aws_security_group", "sg-theirs", "", "aws_security_group.web:b"),
		map[string]string{"name": "something-else"})

	res := classifyIn(t, dir, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{ours, theirs}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_security_group", 2)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_security_group.web["c"]`)}}})

	if len(res.Renames) != 1 {
		t.Fatalf("content did not resolve the ambiguity:\n%s", res)
	}
	if res.Renames[0].LiveID != "sg-ours" {
		t.Errorf("the wrong live resource was paired: %s", res.Renames[0])
	}
}

// TestContentDoesNotResolveAnAmbiguityWithoutEvidence: narrowing several
// possibilities to one by eliminating the others is not the same as knowing
// the survivor is right. With nothing readable to compare, an ambiguity stays
// ambiguous.
func TestContentDoesNotResolveAnAmbiguityWithoutEvidence(t *testing.T) {
	res := classifyFixture(t, discovery.Result{Verdicts: discovery.Verdicts{Orphans: []discovery.OwnedResource{
		orphan("aws_subnet", "subnet-1", "", "aws_subnet.this:a"),
		orphan("aws_subnet", "subnet-2", "", "aws_subnet.this:b"),
	}}, Report: discovery.Report{Scans: []discovery.TypeScan{scan("aws_subnet", 2)}, Unbound: []addrs.AbsResourceInstance{mustAddr(t, `aws_subnet.this["c"]`)}}})

	if len(res.Renames) != 0 {
		t.Errorf("an unreadable ambiguity was resolved anyway:\n%s", res)
	}
	if _, ok := res.AmbiguousFor("aws_subnet.this"); !ok {
		t.Errorf("the ambiguity was not reported:\n%s", res)
	}
}
