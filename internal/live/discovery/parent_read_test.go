// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"os"
	"path/filepath"
	"testing"
)

// The parent-read sweep leg (issue #60): an untaggable, parent-readable
// type found by reading a marked, admitted parent's own identity rather
// than by a marker of its own. These tests use a small, self-contained
// configuration rather than the P0.1 estate fixture, because the fixture
// already declares aws_s3_bucket_policy.data for its one bucket - exactly
// the "already declared" case this leg has to leave alone - and every case
// here needs to control that independently, per bucket.

// parentReadFixture writes a minimal configuration to a temp directory and
// returns it: two buckets (one with a declared policy, one without), one
// role, no live block - none of this leg's machinery reads it, and
// loadConfig does not require one.
func parentReadFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	const src = `
terraform {
  required_version = ">= 1.5.0"
  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = "= 6.58.0"
    }
  }
}

resource "aws_s3_bucket" "data" {
  bucket = "my-bucket"
}

resource "aws_s3_bucket" "other" {
  bucket = "other-bucket"
}

resource "aws_s3_bucket_policy" "other" {
  bucket = aws_s3_bucket.other.id
  policy = "{}"
}

resource "aws_iam_role" "app" {
  name                = "my-role"
  assume_role_policy  = "{}"
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

func discoverParentReadFixture(t *testing.T, cloud *fakeCloud, req Request) *Result {
	t.Helper()

	cfg := loadConfig(t, parentReadFixture(t))
	req.Estate = estateName
	req.Config = cfg
	req.Resolutions = resolveOrFail(t, cfg).All()
	req.Provider = cloud
	req.Sweep = true

	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)
	return res
}

func findParentRead(res *Result, typeName, importID string) (ParentReadFinding, bool) {
	for _, f := range res.ParentReads {
		if f.TypeName == typeName && f.ImportID == importID {
			return f, true
		}
	}
	return ParentReadFinding{}, false
}

// TestParentReadSweepFindsUndeclaredChild is the headline: a bucket that is
// declared and resolves concrete straight from its own config (client-named,
// no marker needed) has a live policy nothing in the configuration
// declares. The leg finds it by reading the bucket's own identity, and -
// aws_s3_bucket_policy being the one type this pass also trusts to remove -
// proposes destroying it at a synthesized address built from the bucket's
// own resource name.
func TestParentReadSweepFindsUndeclaredChild(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_s3_bucket")
	cloud.listableUntagged("aws_s3_bucket_policy")
	cloud.obj("aws_s3_bucket_policy", "my-bucket", nil)
	// "other-bucket" also carries a live policy, but its block is declared
	// in configuration; this must not be reported a second time by this
	// leg, and its presence here is what proves the leg checks per bucket
	// rather than concluding "the type has a declared instance somewhere"
	// and staying out of the whole type the way the ordinary sweep does.
	cloud.obj("aws_s3_bucket_policy", "other-bucket", nil)

	res := discoverParentReadFixture(t, cloud, Request{})

	f, ok := findParentRead(res, "aws_s3_bucket_policy", "my-bucket")
	if !ok {
		t.Fatalf("no parent-read finding for my-bucket's policy:\n%s", res)
	}
	if f.Parent != "aws_s3_bucket" {
		t.Errorf("finding names parent %q, want aws_s3_bucket", f.Parent)
	}
	if f.ParentAddr.String() != "aws_s3_bucket.data" {
		t.Errorf("finding names parent address %q, want aws_s3_bucket.data", f.ParentAddr.String())
	}
	if f.ParentValue != "my-bucket" {
		t.Errorf("finding names parent value %q, want my-bucket", f.ParentValue)
	}
	if !f.Removal {
		t.Errorf("aws_s3_bucket_policy finding is not a removal: %+v", f)
	}
	if f.Withheld != "" {
		t.Errorf("a removal finding carries a withheld reason: %q", f.Withheld)
	}

	if _, ok := findParentRead(res, "aws_s3_bucket_policy", "other-bucket"); ok {
		t.Errorf("the already-declared policy for other-bucket was also reported:\n%s", res)
	}

	// The synthesized address reuses the bucket's own resource name, and a
	// concrete, undeclared resolution rides along with it so the plan
	// engine proposes the destroy.
	var found bool
	for _, r := range res.Resolutions {
		if r.Addr.String() != "aws_s3_bucket_policy.data" {
			continue
		}
		found = true
		if r.ImportID != "my-bucket" {
			t.Errorf("resolution carries import ID %q, want my-bucket", r.ImportID)
		}
		if !r.Undeclared {
			t.Error("the parent-read removal's resolution is not marked Undeclared")
		}
	}
	if !found {
		t.Fatalf("no resolution was produced at aws_s3_bucket_policy.data:\n%s", res)
	}
}

// TestParentReadSweepSkipsDeclaredChild pins the negative of the headline
// case directly: other-bucket's policy is declared, so no finding at all -
// checked on its own rather than only as a side effect of the test above.
func TestParentReadSweepSkipsDeclaredChild(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_s3_bucket")
	cloud.listableUntagged("aws_s3_bucket_policy")
	cloud.obj("aws_s3_bucket_policy", "other-bucket", nil)

	res := discoverParentReadFixture(t, cloud, Request{})

	if len(res.ParentReads) != 0 {
		t.Errorf("a declared child was reported by the parent-read leg:\n%s", res)
	}
}

// TestParentReadSweepNoLiveChild: nothing live at all for my-bucket's
// policy produces no finding and no resolution - "looked and found none"
// stays silent, the same restraint the ordinary sweep shows.
func TestParentReadSweepNoLiveChild(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_s3_bucket")
	cloud.listableUntagged("aws_s3_bucket_policy")

	res := discoverParentReadFixture(t, cloud, Request{})

	if len(res.ParentReads) != 0 {
		t.Errorf("a finding was reported with nothing live:\n%s", res)
	}
}

// TestParentReadSweepReportOnlyType covers a parent-readable,
// single-component type this pass has not wired for removal:
// aws_s3_bucket_versioning is structurally identical to the policy (see
// live/LIMITATIONS.md's parent-read table) but stays report-only, so the
// finding carries no removal and no resolution is added.
func TestParentReadSweepReportOnlyType(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_s3_bucket")
	cloud.listableUntagged("aws_s3_bucket_versioning")
	cloud.obj("aws_s3_bucket_versioning", "my-bucket", nil)

	res := discoverParentReadFixture(t, cloud, Request{})

	f, ok := findParentRead(res, "aws_s3_bucket_versioning", "my-bucket")
	if !ok {
		t.Fatalf("no parent-read finding for my-bucket's versioning:\n%s", res)
	}
	if f.Removal {
		t.Errorf("aws_s3_bucket_versioning is reported as a removal; it is not in identity.ParentReadRemovable")
	}
	if f.Withheld == "" {
		t.Error("a report-only finding carries no withheld reason")
	}

	for _, r := range res.Resolutions {
		if r.Addr.String() == "aws_s3_bucket_versioning.data" {
			t.Errorf("a report-only finding still produced a resolution: %s", r)
		}
	}
}

// TestParentReadSweepIgnoresMultiComponentTypes: aws_iam_role_policy is
// parent-readable (its role argument names aws_iam_role) but not the
// single-component shape this pass acts on - its own policy name is a
// second, free-standing argument the parent alone does not supply. A live
// object of that type is never even read for.
func TestParentReadSweepIgnoresMultiComponentTypes(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_iam_role")
	cloud.listableUntagged("aws_iam_role_policy")
	cloud.obj("aws_iam_role_policy", "my-role:some-inline-policy", nil)

	res := discoverParentReadFixture(t, cloud, Request{})

	if len(res.ParentReads) != 0 {
		t.Errorf("a multi-component parent-readable type was reported by this leg:\n%s", res)
	}
	if len(cloud.requests) != 0 {
		if _, ok := cloud.requestFor("aws_iam_role_policy"); ok {
			t.Errorf("aws_iam_role_policy was listed by the parent-read leg; it should never be reached")
		}
	}
}

// TestParentReadSweepRequiresSweepFlag: the leg is opt-in with the rest of
// the sweep, the same as [TestSweepIsSkippedWithoutTheFlag].
func TestParentReadSweepRequiresSweepFlag(t *testing.T) {
	cloud := newFakeCloud()
	cloud.listable("aws_s3_bucket")
	cloud.listableUntagged("aws_s3_bucket_policy")
	cloud.obj("aws_s3_bucket_policy", "my-bucket", nil)

	cfg := loadConfig(t, parentReadFixture(t))
	req := Request{
		Estate:      estateName,
		Config:      cfg,
		Resolutions: resolveOrFail(t, cfg).All(),
		Provider:    cloud,
		// Sweep left false.
	}
	res, diags := Discover(t.Context(), req)
	assertNoErrors(t, diags)

	if len(res.ParentReads) != 0 {
		t.Errorf("the parent-read leg ran without Request.Sweep:\n%s", res)
	}
}
