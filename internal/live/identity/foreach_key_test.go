// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"testing"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
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
	if !hasDiag(diags, "for_each key cannot be recorded as a marker", `"2001:db8::/64"`) {
		t.Errorf("no diagnostic naming the offending key. got:\n%s", renderDiags(diags))
	}
	if result.Len() != 0 {
		t.Errorf("resolution produced %d instances for a configuration whose keys cannot be recorded", result.Len())
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
