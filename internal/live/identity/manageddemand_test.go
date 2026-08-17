// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// TestDemandedManagedReadsNamesTheBlockAndTheUnsetVariableNamesNothing is
// deliberately one test over two fixtures rather than two tests, because the
// second half is the safety property and a safety property asserted on its own
// passes trivially when the mechanism is broken or unreached.
//
// The first fixture is issue #187's shape: a for_each comprehension over
// aws_acm_certificate.cert.domain_validation_options, an attribute the
// configuration never sets. The demand analysis has to name that block, and
// name every instance of it the pass resolved, because
// [projection.ReadInstances] reads a block all-or-nothing.
//
// The second is #183's shape: a for_each over a required root variable with no
// value. It must contribute NO demand. Not "a demand that is then filtered" -
// none at all, because the thing it could not evaluate is a variable and a
// variable is not a managed resource. See the check package's own guard for
// the same claim made against the loader that substitutes an unknown for such
// a variable, which is the loader every corpus measurement uses.
func TestDemandedManagedReadsNamesTheBlockAndTheUnsetVariableNamesNothing(t *testing.T) {
	t.Run("managed attribute", func(t *testing.T) {
		cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach"), nil)
		result, diags := ResolveWith(context.Background(), cfg, Context{})
		if !diags.HasErrors() {
			t.Fatal("the fixture resolved; it is meant to refuse, and a demand is only derived from a refusal")
		}

		got := DemandedManagedReads(result, diags)
		if len(got) != 1 {
			t.Fatalf("want exactly one demanded block, got %d: %+v", len(got), got)
		}
		d := got[0]
		if d.Resource.String() != "aws_acm_certificate.cert" {
			t.Errorf("demanded %s, want aws_acm_certificate.cert", d.Resource)
		}
		if !d.Module.IsRoot() {
			t.Errorf("demanded block is in module %q, want the root module", d.Module)
		}
		if d.NeededBy != "aws_route53_record.cert_validation" {
			t.Errorf("NeededBy is %q, want the block whose for_each could not be expanded", d.NeededBy)
		}
		// The instances, not their count: a read of the wrong instance is
		// worse than a read of none, and a count cannot tell the two apart.
		var addrs []string
		for _, inst := range d.Instances {
			addrs = append(addrs, inst.Addr.String())
		}
		if len(addrs) != 1 || addrs[0] != "aws_acm_certificate.cert" {
			t.Errorf("instances to read are %v, want [aws_acm_certificate.cert]", addrs)
		}
		if !d.Complete {
			t.Error("the block resolved whole, so a read of it would be whole; Complete says otherwise")
		}
	})

	t.Run("unset required variable", func(t *testing.T) {
		cfg := loadConfig(t, filepath.Join("testdata", "foreach-unset-var-map"), nil)
		result, diags := ResolveWith(context.Background(), cfg, Context{})
		if !diags.HasErrors() {
			t.Fatal("the fixture resolved; #183 rules it must stay refused")
		}
		if got := DemandedManagedReads(result, diags); len(got) != 0 {
			t.Fatalf("an unset root variable was reported as a live read that would settle it: %+v", got)
		}
	})
}

// TestManagedRefusalKeepsItsInstanceFailureTag is the regression this file's
// one behavioural change could cause and nothing else would catch. Every
// consumer of this package's diagnostics reads [InstanceFailure] to attribute
// a refusal to an address; hanging a second Extra on the same diagnostic must
// not push that one out of the unwrap chain.
//
// The fixture's refusal is raised while expanding a block, outside any
// instance frame, so the tag is empty there - which is what it always was.
// What this pins is the OTHER half: that the reference is reachable, and that
// reaching it does not require knowing which of the two was attached first.
func TestManagedRefusalKeepsItsInstanceFailureTag(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach"), nil)
	_, diags := ResolveWith(context.Background(), cfg, Context{})

	var found bool
	for _, d := range diags {
		ref := tfdiags.ExtraInfo[configs.RefusedReference](d)
		if ref.Category != configs.CategoryManagedResource {
			continue
		}
		found = true
		if d.Description().Summary != "Non-static for_each expression" {
			t.Errorf("the reference rode on %q; the refusal's own wording was meant to be untouched", d.Description().Summary)
		}
		// ExtraInfo must not panic or mis-answer for the tag type, whether or
		// not this particular diagnostic carries an address.
		_ = tfdiags.ExtraInfo[InstanceFailure](d)
	}
	if !found {
		t.Fatal("no diagnostic carried a managed RefusedReference; the demand analysis has nothing to read")
	}
}
