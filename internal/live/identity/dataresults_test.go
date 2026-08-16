// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// These tests are issue #179 stage 1's resolution seam, held to the same
// standard the parent-attr and count-length tests set: the exact resolved
// identity with the phase's results present, and the refusal proof with
// them absent - a run with the fix disabled must fail, or the assertion
// proves nothing.

// TestDataResultResolvesIdentityArgument is the corpus's dominant idiom: a
// data source's attribute inside an identity argument, directly and through
// a local.
func TestDataResultResolvesIdentityArgument(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "data-read-identity"), nil)

	results := map[string]cty.Value{
		"data.aws_route53_zone.primary": cty.ObjectVal(map[string]cty.Value{
			"zone_id": cty.StringVal("Z0423220"),
			"name":    cty.StringVal("example.com."),
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{DataResults: results})
	assertNoErrors(t, diags)

	for addr, want := range map[string]string{
		"aws_cloudwatch_log_group.per_zone":  "/zones/Z0423220",
		"aws_cloudwatch_log_group.via_local": "/zones-local/Z0423220",
	} {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Fatalf("%s resolved %s; with the data result present every value it needs is in hand", addr, res.Class)
		}
		if res.ImportID != want {
			t.Errorf("%s resolved to %q, want %q", addr, res.ImportID, want)
		}
	}
}

// TestDataResultResolvesTfeOutputsReference is #179 stage 2's proof that
// the DataResults seam needed no change for the cross-stack flavor: a
// tfe_outputs result, handed in exactly like a same-stack one, resolves the
// identity that reads it. What differs between the two flavors lives
// entirely in internal/live/dataread's eligibility rule, not here.
func TestDataResultResolvesTfeOutputsReference(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "data-read-tfe"), nil)

	results := map[string]cty.Value{
		"data.tfe_outputs.app": cty.ObjectVal(map[string]cty.Value{
			"organization": cty.StringVal("acme"),
			"workspace":    cty.StringVal("prod"),
			"values": cty.MapVal(map[string]cty.Value{
				"log_group": cty.StringVal("prod-app"),
			}),
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{DataResults: results})
	assertNoErrors(t, diags)

	res := resolutionAt(t, result, "aws_cloudwatch_log_group.per_workspace")
	if res.Class != ClassConcrete {
		t.Fatalf("resolved %s; with the data result present every value it needs is in hand", res.Class)
	}
	if want := "/tfe/prod-app"; res.ImportID != want {
		t.Errorf("resolved to %q, want %q", res.ImportID, want)
	}
}

// TestDataReferenceRefusesWithoutResults is the disabled-fix proof: the
// same configuration with no DataResults must refuse exactly as it always
// has, with the passthrough dynamic-value wording, not resolve to anything.
func TestDataReferenceRefusesWithoutResults(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "data-read-identity"), nil)

	result, diags := Resolve(context.Background(), cfg)
	if !diags.HasErrors() {
		t.Fatalf("resolution with no data results accepted a data-source reference in an identity argument")
	}
	found := false
	for _, diag := range diags {
		if diag.Description().Summary == "Dynamic value in static context" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected the existing dynamic-value refusal, got: %s", diags.Err())
	}
	if _, ok := result.Get(mustAddr(t, "aws_cloudwatch_log_group.per_zone")); ok {
		t.Fatalf("aws_cloudwatch_log_group.per_zone resolved despite its data reference being refused")
	}
}

// TestDataResultDrivesForEach: the read value expands a for_each, so its
// elements become instance keys.
func TestDataResultDrivesForEach(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "data-read-foreach"), nil)

	results := map[string]cty.Value{
		"data.aws_availability_zones.here": cty.ObjectVal(map[string]cty.Value{
			"names": cty.ListVal([]cty.Value{cty.StringVal("eu-west-1a"), cty.StringVal("eu-west-1b")}),
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{DataResults: results})
	assertNoErrors(t, diags)

	for _, az := range []string{"eu-west-1a", "eu-west-1b"} {
		addr := `aws_cloudwatch_log_group.per_az["` + az + `"]`
		res := resolutionAt(t, result, addr)
		if res.Class != ClassConcrete {
			t.Fatalf("%s resolved %s, want concrete", addr, res.Class)
		}
		if want := "/az/" + az; res.ImportID != want {
			t.Errorf("%s resolved to %q, want %q", addr, res.ImportID, want)
		}
	}
	if _, ok := result.Get(mustAddr(t, `aws_cloudwatch_log_group.per_az["eu-west-1c"]`)); ok {
		t.Fatalf("an instance exists for a key the data result does not carry")
	}
}

// TestUnusableDataResultIsACallerError: a key resolution cannot index is
// reported, never dropped - dropped, it would resurface as the generic
// dynamic-value refusal pointing the user at their own configuration.
func TestUnusableDataResultIsACallerError(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "data-read-identity"), nil)

	results := map[string]cty.Value{
		"not an address": cty.StringVal("x"),
		"data.aws_route53_zone.primary": cty.ObjectVal(map[string]cty.Value{
			"zone_id": cty.StringVal("Z0423220"),
			"name":    cty.StringVal("example.com."),
		}),
	}

	_, diags := ResolveWith(context.Background(), cfg, Context{DataResults: results})
	found := false
	for _, diag := range diags {
		if diag.Description().Summary == SummaryUnusableDataResult {
			found = true
			if !strings.Contains(diag.Description().Detail, "not an address") {
				t.Errorf("the caller-error refusal does not name the offending key: %s", diag.Description().Detail)
			}
		}
	}
	if !found {
		t.Fatalf("an unparseable data-result key was silently dropped; diags: %s", diags.Err())
	}
}

// TestAggregateShapes pins the aggregation rule: single object for NoKey,
// tuple in index order for count, object for for_each, and a refusal for
// mixed key kinds or holes.
func TestAggregateShapes(t *testing.T) {
	one := cty.StringVal("one")
	two := cty.StringVal("two")

	if v, ok := aggregateGroup(&one, nil, nil); !ok || v != one {
		t.Errorf("NoKey aggregate: got %#v, %v", v, ok)
	}
	if v, ok := aggregateGroup(nil, map[int]cty.Value{0: one, 1: two}, nil); !ok || !v.RawEquals(cty.TupleVal([]cty.Value{one, two})) {
		t.Errorf("count aggregate: got %#v, %v", v, ok)
	}
	if v, ok := aggregateGroup(nil, nil, map[string]cty.Value{"a": one}); !ok || !v.RawEquals(cty.ObjectVal(map[string]cty.Value{"a": one})) {
		t.Errorf("for_each aggregate: got %#v, %v", v, ok)
	}
	if _, ok := aggregateGroup(&one, map[int]cty.Value{0: two}, nil); ok {
		t.Errorf("mixed key kinds aggregated; they have no honest shape")
	}
	if _, ok := aggregateGroup(nil, map[int]cty.Value{1: two}, nil); ok {
		t.Errorf("a count sequence with a hole aggregated")
	}
}
