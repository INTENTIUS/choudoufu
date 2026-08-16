// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
)

// TestForEachKeyRefusedAtExpansion is the resolver's half of finding F-FE.
// Lint catches the for_each expressions it can evaluate from the
// configuration text; this is the point where a key has actually been
// computed and is one step from becoming an address, a marker, and a wedge.
// A run that reached here without passing lint still must not produce a
// resolution.
func TestForEachKeyRefusedAtExpansion(t *testing.T) {
	cfg := loadConfig(t, "testdata/foreach-bad-key", nil)
	result, diags := Resolve(context.Background(), cfg)

	if !diags.HasErrors() {
		t.Fatalf("no error diagnostics; resolution produced %d instances", result.Len())
	}
	if !hasDiag(diags, "for_each key cannot be recorded as a marker", `"50%full"`) {
		t.Errorf("no diagnostic naming the offending key. got:\n%s", renderDiags(diags))
	}
	if result.Len() != 0 {
		t.Errorf("resolution produced %d instances for a configuration whose keys cannot be recorded", result.Len())
	}
}

// TestForEachKeyOverDataRefusesEncodeNeedingKey is issue #227's real gap:
// a for_each rooted at a data source produces a key that is
// [markerkey.Valid] (every rune is printable and none is Excluded) but
// still needs [markerkey.Encode]'s help - a literal "(" - and stamp cannot
// re-derive this expression statically to build the escape table Encode's
// help requires (see [stampCanReadStatically]), so it would fall back to a
// narrower template and mis-stamp the key silently. This must refuse here,
// at the one place that has the resolved value, rather than let the run
// reach stamp.
//
// Reuses testdata/data-read-foreach, the same `toset(data.*.names)` shape
// TestDataResultDrivesForEach already exercises with safe values - this
// test's only difference is a key data legitimately carries (a report
// filename, an S3-object-key shape) that TestDataResultDrivesForEach's
// availability-zone names never would.
func TestForEachKeyOverDataRefusesEncodeNeedingKey(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "data-read-foreach"), nil)

	results := map[string]cty.Value{
		"data.aws_availability_zones.here": cty.ObjectVal(map[string]cty.Value{
			"names": cty.ListVal([]cty.Value{cty.StringVal("reports(draft).json")}),
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{DataResults: results})
	if !diags.HasErrors() {
		t.Fatalf("no error diagnostics; resolution produced %d instances for a data-rooted for_each key needing Encode's help", result.Len())
	}
	if !hasDiag(diags, "for_each key cannot be recorded as a marker", `"reports(draft).json"`) {
		t.Errorf("no diagnostic naming the offending key. got:\n%s", renderDiags(diags))
	}
	if result.Len() != 0 {
		t.Errorf("resolution produced %d instances for a configuration whose for_each key cannot be recorded", result.Len())
	}
}

// TestForEachKeyOverDataAcceptsLegalRuneKey is
// TestForEachKeyOverDataRefusesEncodeNeedingKey's control: a data-rooted
// for_each key that never needed Encode's help (every rune already
// legalRune) must keep resolving. This is the corpus's own shape - subnet
// IDs, VPC IDs, region names, "owner/repo" strings - and #227's own
// investigation is only a safe fix if none of it newly refuses.
func TestForEachKeyOverDataAcceptsLegalRuneKey(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "data-read-foreach"), nil)

	results := map[string]cty.Value{
		"data.aws_availability_zones.here": cty.ObjectVal(map[string]cty.Value{
			"names": cty.ListVal([]cty.Value{cty.StringVal("eu-west-1a"), cty.StringVal("eu-west-1b")}),
		}),
	}

	result, diags := ResolveWith(context.Background(), cfg, Context{DataResults: results})
	assertNoErrors(t, diags)
	if result.Len() != 2 {
		t.Errorf("resolution produced %d instances, want 2 for two legalRune data-rooted for_each keys", result.Len())
	}
}

// parseExprForTest parses a bare HCL expression, for the tests that exercise
// the expression walkers directly.
func parseExprForTest(t *testing.T, src string) hcl.Expression {
	t.Helper()

	expr, diags := hclsyntax.ParseExpression([]byte(src), "test.tf", hcl.InitialPos)
	if diags.HasErrors() {
		t.Fatalf("parsing %q: %s", src, diags.Error())
	}
	return expr
}
