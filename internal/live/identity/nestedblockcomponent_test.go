// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"
)

// TestNestedBlockComponent proves [Component.Block] against
// aws_autoscaling_traffic_source_attachment (GitHub issue #310), the type it
// was built for: a fully client-specified, comma-composite import ID whose
// second and third segments are the `type` and `identifier` attributes of a
// required, max_items:1 `traffic_source` nested block rather than top-level
// arguments.
//
// Both directions matter equally. The resolving case proves the mechanism
// reaches into the block at all; the refusing case is the mutation check -
// a resolver that silently treated an absent required block as an empty one,
// or fabricated a default, would over-admit exactly the shape "a wrong
// marker outranks a missing one" warns about.
func TestNestedBlockComponent(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "nested-block-component"), nil)
	result, diags := Resolve(context.Background(), cfg)

	present := resolutionAt(t, result, "aws_autoscaling_traffic_source_attachment.present")
	if present.Class != ClassConcrete {
		t.Fatalf("present resolved %s, want concrete (diagnostics: %s)", present.Class, renderDiags(diags))
	}
	// The provider's own documented import example
	// (aws_autoscaling_traffic_source_attachment.html.markdown's Import
	// section), verbatim - proof the block's own leaves, not some
	// fabricated stand-in, supplied the second and third segments.
	if want := "example,elbv2,arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/example/1234567890123456"; present.ImportID != want {
		t.Errorf("present rendered %q, want %q", present.ImportID, want)
	}
	if got, want := present.IdentityValues["type"], "elbv2"; got != want {
		t.Errorf("present's type identity value = %q, want %q", got, want)
	}
	if got, want := present.IdentityValues["identifier"], "arn:aws:elasticloadbalancing:us-west-2:123456789012:targetgroup/example/1234567890123456"; got != want {
		t.Errorf("present's identifier identity value = %q, want %q", got, want)
	}

	// The mutation check: no traffic_source block at all must refuse - the
	// same "Identity argument not set" diagnostic a missing top-level
	// required argument gets - not resolve to a partial or fabricated
	// identity.
	if !diags.HasErrors() {
		t.Fatalf("no error diagnostics for the block-absent instance; resolution produced %d instances", result.Len())
	}
	if !hasDiag(diags, "Identity argument not set", `aws_autoscaling_traffic_source_attachment.absent`) {
		t.Errorf("no diagnostic naming the block-absent instance. got:\n%s", renderDiags(diags))
	}
	if _, ok := result.Get(mustAddr(t, "aws_autoscaling_traffic_source_attachment.absent")); ok {
		t.Errorf("absent was resolved anyway; a missing required nested block must be refused, not guessed")
	}

	// The second mutation check: the block is present, but one leaf is built
	// from an impure call (uuid()). A Block-sourced leaf that bypassed the
	// ordinary resolveExpr machinery would fabricate a fresh identity on
	// every run instead of refusing - the same failure
	// TestImpureFunctionIdentityRefused pins for a top-level argument.
	if !hasDiag(diags, "Identity derived from an impure function", "uuid()") {
		t.Errorf("no diagnostic naming the impure block leaf. got:\n%s", renderDiags(diags))
	}
	if res, ok := result.Get(mustAddr(t, "aws_autoscaling_traffic_source_attachment.impure")); ok {
		t.Errorf("impure resolved anyway, as %s with import ID %q: a fabricated identity is the failure this case exists for", res.Class, res.ImportID)
	}
}
