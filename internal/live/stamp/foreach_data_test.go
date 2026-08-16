// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package stamp

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// GitHub issue #227: a for_each rooted at a data source can be stamped with
// the pre-#210 unescaped template, which only reproduces the escaping rule
// for "@", "." and ":" - not the general [markerkey.Encode] a key outside
// that narrower set needs - because [stamper.staticForEachKeys] cannot see
// a data-rooted for_each's actual keys to know [forEachNeedsKeyLookup]
// should switch it to the lookup-table path.
//
// The open question the issue itself flagged: does anything ELSE already
// catch a bad key before stamp ever sees it? [identity]'s own resolver has
// its own copy of the same rule ([markerkey.InvalidRune], via
// checkedForEachKeys), run against the ACTUAL resolved value once the
// data-read phase's results are wired into its own scoped evaluator - not
// against the for_each expression's text the way lint and stamp's
// traversal pre-filters do. TestIdentityRefusesBadDataForEachKey answers
// that question first, because if identity already refuses, the run never
// reaches stamp with a bad key in practice and the "silent mis-bind" is not
// reachable end to end, whatever stamp's own template does in isolation.

// dataForEachFixture is one resource whose for_each is rooted at a data
// source's plural string attribute - the shape the issue names as the
// realistic trigger (an S3 object-key listing, "toset(data.aws_s3_objects.
// this.keys)"), simplified to a fixture data source since aws_s3_objects
// carries no test schema here and the resolver never inspects a data
// source's OWN schema - only the caller-supplied DataResults value.
const dataForEachFixture = `
data "aws_s3_objects" "this" {
  bucket = "irrelevant-to-this-test"
}

resource "aws_cloudwatch_log_group" "per_key" {
  for_each = toset(data.aws_s3_objects.this.keys)

  name = each.key
}
`

// TestIdentityRefusesBadDataForEachKey is the end-to-end premise check: a
// for_each key drawn from a data source, containing one of
// [markerkey.Excluded]'s six characters, resolved through
// identity.ResolveWith exactly the way live_plan.go and live_mode.go call
// it (DataResults populated, nothing else).
//
// If this refuses, issue #227's central claim - that identity can resolve
// such a for_each while stamp and lint cannot see through it - is only half
// true: identity CAN resolve it, but the resolution itself already carries
// [markerkey.InvalidRune]'s check against the real key, so the run never
// reaches stamp.Stamp with an unsafe key. If this resolves cleanly instead,
// the claim holds all the way through and stamp really is the last line of
// defense - which TestStamp_dataForEachSilentlyMisbinds (below) shows
// failing.
func TestIdentityRefusesBadDataForEachKey(t *testing.T) {
	cfg := loadSource(t, dataForEachFixture)

	results := map[string]cty.Value{
		"data.aws_s3_objects.this": cty.ObjectVal(map[string]cty.Value{
			"keys": cty.ListVal([]cty.Value{
				cty.StringVal("reports/2024/q1$summary.json"),
				cty.StringVal("reports/2024/q2-summary.json"),
			}),
		}),
	}

	_, diags := identity.ResolveWith(context.Background(), cfg, identity.Context{DataResults: results})

	if !diags.HasErrors() {
		t.Fatalf(
			"identity.ResolveWith accepted a data-rooted for_each whose resolved key %q contains %q, a character "+
				"markerkey.Excluded lists; this means an end-to-end live-plan/live-mode run reaches stamp.Stamp with "+
				"an unsafe key, and the mis-bind TestStamp_dataForEachSilentlyMisbinds demonstrates is reachable in "+
				"practice, not merely in isolation",
			"reports/2024/q1$summary.json", "$")
	}

	found := false
	for _, diag := range diags {
		if diag.Description().Summary == "for_each key cannot be recorded as a marker" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected identity's own markerkey.InvalidRune refusal (checkedForEachKeys), got: %s", diags.Err())
	}
}

// TestStamp_dataForEachSilentlyMisbinds is stamp's own half of the same
// question, run the way every existing foreach_escape_test.go case is run:
// stamp.Stamp called directly over the fixture's text, the same code path
// TestStamp_eachKeyEscapingRoundTrips and TestStamp_rejectedEachKeysWouldMisbind
// already exercise for a STATIC for_each.
//
// Before this package's own SkipDataForEachKey fix, this proved the mis-bind
// stamp.Stamp would commit if it were ever reached with a bad key: the
// per-instance fallback template only escapes "@", "." and ":", so a key
// containing "$" sails through unescaped, and the marker stamped for
// each.key == "reports/2024/q1$summary.json" does not match the address
// OpenTofu itself would render for that instance. After the fix, this
// resource is refused outright instead - stamp.Stamp reports an error
// rather than writing anything - which this test now asserts alongside the
// unpatched mechanism's own byte-for-byte proof, so a regression that
// silently dropped the refusal would still be caught by the first half.
func TestStamp_dataForEachSilentlyMisbinds(t *testing.T) {
	cfg := loadSource(t, dataForEachFixture)

	if lint.ValidForEachKey("reports/2024/q1$summary.json") {
		t.Fatalf(`"reports/2024/q1$summary.json" is admitted by the lint rule; this test's premise is wrong`)
	}

	res, diags := Stamp(t.Context(), Request{
		Estate:  "stamp-unit",
		Config:  cfg,
		Schemas: testSchemas(),
		NeedsDiscovery: map[string]bool{
			"aws_cloudwatch_log_group.per_key": true,
		},
	})

	if !diags.HasErrors() {
		t.Fatalf("stamp.Stamp accepted a data-rooted for_each with no way to verify its keys; want a refusal (SkipDataForEachKey)")
	}

	foundSkip := false
	for _, skip := range res.Skipped {
		if skip.Reason == SkipDataForEachKey {
			foundSkip = true
		}
	}
	if !foundSkip {
		t.Fatalf("stamp.Stamp refused for some other reason; want SkipDataForEachKey. Skips: %+v", res.Skipped)
	}
}

// TestIdentityAcceptsEncodableButNonLegalKey narrows the corrected claim:
// identity's own checkedForEachKeys only refuses the six markerkey.Excluded
// characters (the ones Encode structurally cannot represent). A key that
// needs Encode's hex-escape help WITHOUT being one of those six - a
// parenthesis, say, admitted by issue #210's widening - is still
// [markerkey.Valid], so identity resolves it cleanly. That is exactly the
// gap [SkipDataForEachKey] exists to close: this key sails through
// identity with no refusal at all, and only stamp's own inability to build
// a lookup table for a data-rooted for_each's unknowable keys is what
// stood between it and a silent mis-bind before this package's fix.
func TestIdentityAcceptsEncodableButNonLegalKey(t *testing.T) {
	cfg := loadSource(t, `
data "aws_s3_objects" "this" {
  bucket = "irrelevant-to-this-test"
}

resource "aws_cloudwatch_log_group" "per_key" {
  for_each = toset(data.aws_s3_objects.this.keys)

  name = each.key
}
`)

	results := map[string]cty.Value{
		"data.aws_s3_objects.this": cty.ObjectVal(map[string]cty.Value{
			"keys": cty.ListVal([]cty.Value{
				cty.StringVal("reports(draft).json"),
			}),
		}),
	}

	if !lint.ValidForEachKey("reports(draft).json") {
		t.Fatalf(`"reports(draft).json" is not admitted by the lint rule; this test's premise is wrong`)
	}

	_, diags := identity.ResolveWith(context.Background(), cfg, identity.Context{DataResults: results})
	if diags.HasErrors() {
		t.Fatalf("identity.ResolveWith refused a key markerkey.Valid admits (%q); the corrected claim is wrong: %s", "reports(draft).json", diags.Err())
	}
}
