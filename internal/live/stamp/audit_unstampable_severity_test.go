// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestUnstampableSeverityFollowsMustStampEverywhere covers the three paths
// into [stamper.unstampableAt] that have no mustStamp gate of their own.
//
// Severity was decided in two places. [stamper.mustStamp] asks whether a
// discovery verdict is present AND does not bind by name; unstampableAt
// asked only whether one is present. Three of the six call sites reach
// unstampableAt without the gate - SkipNotHCL (stamp.go's JSON-syntax
// branch), SkipTagsUnreadable and SkipMarkerUnreadable - so a unique-name
// type written in JSON syntax, or with a tags argument this pass cannot
// read, got the hard "you will never see it again" error that the
// unique-name exemption exists to prevent. It is findable by its name; the
// honest report is a warning. Nothing was watching, and the fix was
// test-safe when it landed. Issue #285.
//
// Two things make this a guard rather than a restatement.
//
// It iterates [identity.AllDiscoveryCauses] and computes the expected
// severity from [identity.DiscoveryCause.BindsByName] - the identity
// package's own answer to "can this be recognised without a marker", which
// is the question severity is meant to be answering. A cause added there
// lands here automatically, in whichever direction it binds.
//
// And it asserts on the RENDERED diagnostic for the address under test, not
// on a predicate and not on Result. Both of the shipped defects this issue
// collects reported success while writing or refusing the wrong thing, so a
// verdict-level check is exactly the check that would not have caught them.
func TestUnstampableSeverityFollowsMustStampEverywhere(t *testing.T) {
	type path struct {
		name   string
		addr   string
		reason SkipReason
		files  map[string]string
	}

	// One resource per path, each of a taggable type - the untaggable branch
	// has a mustStamp gate already and is pinned in discoverycause_test.go.
	// These three do not, which is the whole point.
	paths := []path{{
		name:   "JSON syntax",
		addr:   "aws_s3_bucket.data",
		reason: SkipNotHCL,
		files: map[string]string{"main.tf.json": `{
  "resource": {
    "aws_s3_bucket": {
      "data": {
        "bucket": "stamp-unit-data"
      }
    }
  }
}`},
	}, {
		name:   "tags neither readable nor mergeable",
		addr:   "aws_vpc.main",
		reason: SkipTagsUnreadable,
		files: map[string]string{"main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"
  tags       = "not-a-map-at-all"
}
`},
	}, {
		name:   "hand-written marker this pass cannot read",
		addr:   "aws_vpc.main",
		reason: SkipMarkerUnreadable,
		files: map[string]string{"main.tf": `
resource "aws_vpc" "main" {
  cidr_block = "10.42.0.0/16"

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = aws_s3_bucket.data.bucket
  }
}

resource "aws_s3_bucket" "data" {
  bucket = "b"

  tags = {
    tofu-estate  = "stamp-unit"
    tofu-address = "aws_s3_bucket.data"
  }
}
`},
	}}

	// Args for the causes whose sentence needs them; the rest take none.
	args := map[identity.DiscoveryCause][]string{
		identity.DiscoveryCloudUnknown: {string(identity.CloudAccountID)},
		identity.DiscoveryNameOmitted:  {"name"},
		identity.DiscoveryNamePrefix:   {"name", "name_prefix"},
		identity.DiscoverySiblingApply: {"aws_acm_certificate.cert", "name"},
		identity.DiscoveryUniqueName:   {"name"},
	}

	sawBinding := false
	sawNonBinding := false

	for _, p := range paths {
		for _, cause := range identity.AllDiscoveryCauses() {
			t.Run(p.name+"/"+string(cause), func(t *testing.T) {
				cfg := loadTree(t, p.files)
				res, diags := Stamp(t.Context(), Request{
					Estate:  "stamp-unit",
					Config:  cfg,
					Schemas: testSchemas(),
					NeedsDiscovery: map[string]identity.BlockDiscovery{
						p.addr: {Cause: cause, Args: args[cause]},
					},
				})

				// The premise: this fixture really does reach the path
				// under test. Without it, a fixture that stopped
				// producing the skip would leave the whole case
				// vacuously green - the shape the audit calls "a guard
				// that passes because it is unreached".
				if !hasSkip(res, p.addr, p.reason) {
					t.Fatalf("fixture did not reach %s for %s; skips were %v", p.reason, p.addr, res.Skipped)
				}

				// The external source. Severity is meant to say "can a
				// later run find this without a marker", and identity is
				// where that is decided.
				wantError := !cause.BindsByName()
				if wantError {
					sawNonBinding = true
				} else {
					sawBinding = true
				}

				got, found := severityFor(diags, p.addr)
				if !found {
					t.Fatalf("no diagnostic mentions %s at all; %s became silence:\n%s", p.addr, p.reason, diags.ErrWithWarnings())
				}
				want := tfdiags.Warning
				if wantError {
					want = tfdiags.Error
				}
				if got != want {
					t.Errorf("%s with cause %s reported severity %v, want %v.\n"+
						"Severity is mustStamp's answer: BindsByName()=%v means a later run finds this resource by its name, marker or no marker.\n%s",
						p.addr, cause, got, want, cause.BindsByName(), diags.ErrWithWarnings())
				}
			})
		}
	}

	// Both halves have to have been exercised, or the loop above proves only
	// one direction. AllDiscoveryCauses currently carries exactly one
	// binding cause; if that changes to none, this test stops covering the
	// defect it was written for and says so.
	if !sawBinding {
		t.Error("no cause in identity.AllDiscoveryCauses binds by name; this test no longer covers the exemption it guards")
	}
	if !sawNonBinding {
		t.Error("every cause binds by name; this test no longer proves anything is refused")
	}
}

// severityFor is the severity of the diagnostic about one address. The
// fixtures declare a second resource in one case, and a whole-run
// HasErrors() would read that resource's verdict as this one's.
func severityFor(diags tfdiags.Diagnostics, addr string) (tfdiags.Severity, bool) {
	worst := tfdiags.Warning
	found := false
	for _, d := range diags {
		desc := d.Description()
		if !strings.Contains(desc.Detail, addr) {
			continue
		}
		found = true
		if d.Severity() == tfdiags.Error {
			worst = tfdiags.Error
		}
	}
	return worst, found
}
