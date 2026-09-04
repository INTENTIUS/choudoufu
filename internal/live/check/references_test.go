// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// TestReferencesFindsTheMarkerFilteredDataSource is GitHub issue #790's own
// "Done when": live/e2e/estate-references carries exactly the shape
// live/OUTPUTS.md documents - a data source filtered on a producer's
// tag:tofu-estate and tag:tofu-address, read by one resource in the same
// module - and Analyze (via [Dir]) has to turn it into exactly one
// [Reference], naming the reader.
func TestReferencesFindsTheMarkerFilteredDataSource(t *testing.T) {
	root := flocitest.RepoRoot(t)
	dir := filepath.Join(root, "live", "e2e", "estate-references")

	report := Dir(context.Background(), dir, Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}

	if len(report.References) != 1 {
		t.Fatalf("got %d references, want 1: %+v", len(report.References), report.References)
	}
	ref := report.References[0]
	if ref.From != "data.aws_vpc.network" {
		t.Errorf("From = %q, want %q", ref.From, "data.aws_vpc.network")
	}
	if ref.Estate != "estate-references-network" {
		t.Errorf("Estate = %q, want the tag:tofu-estate filter's value", ref.Estate)
	}
	if ref.Address != "aws_vpc.main" {
		t.Errorf("Address = %q, want the tag:tofu-address filter's value", ref.Address)
	}
	if len(ref.ReadBy) != 1 || ref.ReadBy[0] != "aws_subnet.app" {
		t.Errorf("ReadBy = %v, want [\"aws_subnet.app\"] - vpc_id is not an identity argument of aws_subnet, "+
			"so nothing but this direct HCL walk would have found the read", ref.ReadBy)
	}

	// The reading resource still resolves - its identity does not depend on
	// the cross-estate read at all, so the roster's other half (#790's
	// instances[]) should carry it as an ordinary tag-governable instance.
	var found bool
	for _, inst := range report.Roster {
		if inst.Address != "aws_subnet.app" {
			continue
		}
		found = true
		if inst.Refused {
			t.Errorf("aws_subnet.app is marked refused: %+v", inst)
		}
	}
	if !found {
		t.Errorf("aws_subnet.app is not in the roster at all: %+v", report.Roster)
	}
}

// TestReferencesEmptyWhenNoFilterNamesAnEstate is the negative control: an
// ordinary data source with no tag:tofu-estate filter is not a cross-estate
// edge, and must not be reported as one.
func TestReferencesEmptyWhenNoFilterNamesAnEstate(t *testing.T) {
	dir := t.TempDir()
	body := `
data "aws_ami" "latest" {
  most_recent = true
  owners      = ["self"]

  filter {
    name   = "name"
    values = ["my-ami-*"]
  }
}
`
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}

	report := Dir(context.Background(), dir, Context{})
	if !report.Readable() {
		t.Fatalf("fixture did not load: %s", report.Load.Diags.Error())
	}
	if len(report.References) != 0 {
		t.Errorf("got %d references for a data source with no tag:tofu-estate filter, want 0: %+v", len(report.References), report.References)
	}
}
