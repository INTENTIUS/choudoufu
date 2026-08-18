// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the guard [identity.TestForEachUnsetVariableStillRefuses]'s doc
// comment says belongs here and did not exist: the one that runs against THIS
// package's loader.
//
// The difference is the whole point. internal/live/identity's own test loader
// lets [configs.StaticEvaluator] refuse an unset required root variable
// outright, so a value never reaches the rules at all. load.go here does not:
// it substitutes cty.UnknownVal(variable.ConstraintType) so a corpus run can
// measure a configuration rather than the absence of a tfvars file, and this
// is the loader tools/refusal-probe, "just corpus" and live-check all take.
//
// So under this loader, and only under this loader, an unset variable and a
// value nothing has read yet arrive as the same cty.Unknown. Any rule that
// treats an unknown as "a live read would settle this" reclassifies #183's
// whole cohort - 177 refused sites - which live/corpus-manifest.json rules
// must stay blocked.
//
// Measured on these fixtures at the time this was written:
//
//	foreach-unset-var-map   Non-static for_each expression   0 instances
//	unset-var-identity-arg  Non-static identity argument     0 instances
//
// The second is the collision that matters. "Non-static identity argument" is
// [identity.resolver.stringValue]'s !IsWhollyKnown branch, which is exactly
// where an identity argument left unknown by a live read would land too.

// TestNoManagedDemandFromAnUnsetVariable is the safety half, with its own
// positive control in the same function so it cannot pass by being unreached.
//
// The control has to come first when read and is checked first when run: if
// the demand analysis names nothing for a configuration that genuinely needs a
// live read, then it names nothing for anything, and the two unset-variable
// subtests below are proving that the mechanism is broken rather than that it
// is careful.
func TestNoManagedDemandFromAnUnsetVariable(t *testing.T) {
	t.Run("control: a managed attribute IS demanded", func(t *testing.T) {
		load, result, diags := resolveFixture(t, "managed-result-foreach")
		if len(load.UnsetVariables()) != 0 {
			t.Fatalf("the control has unset variables %v, so it cannot separate the two kinds of unknown", load.UnsetVariables())
		}
		if !diags.HasErrors() {
			t.Fatal("the control fixture resolved with no managed results; it is meant to refuse")
		}
		got := identity.DemandedManagedReads(result, diags)
		if len(got) != 1 || got[0].Resource.String() != "aws_acm_certificate.cert" {
			t.Fatalf("the control demanded %+v, want exactly aws_acm_certificate.cert; "+
				"the guards below mean nothing while this is wrong", got)
		}
	})

	// All three #183 shapes, because they refuse at different places. The
	// for_each one never expands a block at all; the argument one expands and
	// then refuses per instance, in the very branch a live-read unknown would
	// reach; and modulearg-unset-var carries the unknown ACROSS A MODULE
	// BOUNDARY, where the partial-argument rebuild
	// (internal/live/identity/partialargs.go) is the last thing that could
	// invent a key set out of it. That third one exists because the rebuild
	// substitutes an unknown for a leaf it cannot evaluate, and the whole
	// safety argument for doing so is that an unknown ALREADY refuses - so
	// the one shape that must never move is the one whose unknown was there
	// before the rebuild existed.
	for _, dir := range []string{"foreach-unset-var-map", "unset-var-identity-arg", "modulearg-unset-var"} {
		t.Run(dir, func(t *testing.T) {
			load, result, diags := resolveFixture(t, dir)
			if len(load.UnsetVariables()) == 0 {
				t.Fatal("this fixture has no unset required variable, so it is not testing the substituted-unknown path at all")
			}
			if !diags.HasErrors() {
				t.Fatal("resolved a configuration reading a required root variable with no value; #183 rules it must stay refused")
			}
			if got := identity.DemandedManagedReads(result, diags); len(got) != 0 {
				t.Fatalf("an unset root variable was reported as a live read that would settle it: %+v", got)
			}
			// Not Blocked() alone: a rule could leave the run blocked and
			// still have invented instances for a block whose key set nothing
			// knows. Assert there are none.
			for _, res := range result.All() {
				if res.Addr.Resource.Resource.Type == "aws_s3_bucket" {
					t.Errorf("%s was resolved anyway, as %v with import ID %q", res.Addr, res.Class, res.ImportID)
				}
			}
		})
	}
}

// planCertResult is the certificate as the AWS provider PLANS it, which is
// what projection.PlanInstances hands back and what a live-plan run holds
// between its two resolution passes: domain_name filled in from the
// configuration, and the DNS record the operator has to publish still unknown
// until the certificate is applied.
//
// Measured against the real provider (6.60.0) in internal/live/projection's
// TestPlanInstancesAgainstTheAWSProvider, which asserts both halves - the
// known domain_name and the unknown resource_record_name - so this
// hand-written value cannot drift into describing a provider that does not
// exist.
//
// Distinct from certResult in managedresults_test.go, which is the same
// object after a READ and is wholly known.
func planCertResult() map[string]cty.Value {
	return map[string]cty.Value{
		"aws_acm_certificate.cert": cty.ObjectVal(map[string]cty.Value{
			"domain_name": cty.StringVal("example.com"),
			"domain_validation_options": cty.SetVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"domain_name":           cty.StringVal("example.com"),
				"resource_record_name":  cty.UnknownVal(cty.String),
				"resource_record_type":  cty.UnknownVal(cty.String),
				"resource_record_value": cty.UnknownVal(cty.String),
			})}),
		}),
	}
}

// TestSiblingApplyUnderTheSubstitutingLoader is the guard
// [identity.TestForEachUnsetVariableStillRefuses]'s doc comment asks for and
// that identity's own tests cannot provide: the one that runs the whole
// collision under THIS package's loader, where an unset required variable and
// a value waiting on a sibling's apply arrive as the same cty.Unknown.
//
// The control comes first and is what makes the rest mean anything. If the
// classification does not fire for a configuration that genuinely earns it,
// every guard below is passing because the mechanism is unreachable.
func TestSiblingApplyUnderTheSubstitutingLoader(t *testing.T) {
	t.Run("control: the sibling-apply case IS classified", func(t *testing.T) {
		load := Load(context.Background(), filepath.Join("testdata", "managed-result-foreach"))
		if load.Config == nil {
			t.Fatalf("fixture did not load: %s", load.Diags.Error())
		}
		result, diags := identity.ResolveWith(context.Background(), load.Config, identity.Context{
			ManagedResults: planCertResult(),
		})
		// Read AFTER resolution, never before: the loader records a variable
		// as unset when the static evaluator asks for it, which only happens
		// while an expression that reads it is being evaluated. Asked before,
		// this returns empty for every fixture there is and the check is
		// vacuous.
		if len(load.UnsetVariables()) != 0 {
			t.Fatalf("the control has unset variables %v, so it cannot separate the two kinds of unknown", load.UnsetVariables())
		}
		if diags.HasErrors() {
			t.Fatalf("refused with the certificate's PLANNED values in hand: %s", diags.Err())
		}
		var got []string
		for _, res := range result.All() {
			if res.Addr.Resource.Resource.Type != "aws_route53_record" {
				continue
			}
			got = append(got, fmt.Sprintf("%s %s %q cause=%s args=%v",
				res.Addr, res.Class, res.ImportID, res.Cause, res.CauseArgs))
		}
		sort.Strings(got)
		// The rendered value, not the verdict. An instance that came back
		// with a plausible-looking import ID would be worse than the refusal
		// it replaced, and Blocked() cannot see the difference.
		want := `aws_route53_record.cert_validation["example.com"] NEEDS_DISCOVERY "" cause=SIBLING_APPLY args=[aws_acm_certificate.cert name type]`
		if len(got) != 1 || got[0] != want {
			t.Fatalf("classified %v, want exactly:\n  %s\nthe guards below prove nothing while this is wrong", got, want)
		}
	})

	// The same shape, one argument moved onto an unset required variable.
	// Everything the classification keys on is present - managed results in
	// hand, a for_each provably derived from them - and the answer must still
	// be a refusal.
	// managed-result-foreach-unset-var: the argument names no each.* at all,
	// so neither leg of the discriminator can reach it.
	//
	// managed-result-foreach-mixed-var: the argument names BOTH, in one
	// template, so the classification genuinely can reach it and is stopped
	// only by the rule that never attributes an expression naming a variable.
	// The pair matters - a mutation run that flipped that rule off left the
	// first fixture passing, because the guard it was meant to exercise was
	// never on the path.
	for _, dir := range []string{"managed-result-foreach-unset-var", "managed-result-foreach-mixed-var"} {
		t.Run(dir, func(t *testing.T) {
			load := Load(context.Background(), filepath.Join("testdata", dir))
			if load.Config == nil {
				t.Fatalf("fixture did not load: %s", load.Diags.Error())
			}
			result, diags := identity.ResolveWith(context.Background(), load.Config, identity.Context{
				ManagedResults: planCertResult(),
			})
			if len(load.UnsetVariables()) == 0 {
				t.Fatal("resolution never read a required root variable with no value, so this is not testing the substituted-unknown path at all")
			}
			if !diags.HasErrors() {
				t.Fatal("an identity argument reading a required root variable with no value was accepted; #183 rules it must stay refused")
			}
			for _, res := range result.All() {
				if res.Addr.Resource.Resource.Type == "aws_route53_record" {
					t.Errorf("%s was classified as %v (cause %s, import ID %q) from an unset variable",
						res.Addr, res.Class, res.Cause, res.ImportID)
				}
			}
		})
	}

	// And the #183 cohort's own three shapes, resolved WITH managed results
	// supplied. This is the mutation the guard exists for: hand the
	// classification the one condition it keys on and check that nothing in
	// the cohort moves. With an empty Context these three pass whether or not
	// the discriminator works at all, which is why they are run twice.
	for _, dir := range []string{"foreach-unset-var-map", "unset-var-identity-arg", "modulearg-unset-var"} {
		t.Run(dir+"/with-managed-results", func(t *testing.T) {
			load := Load(context.Background(), filepath.Join("testdata", dir))
			if load.Config == nil {
				t.Fatalf("fixture %s did not load: %s", dir, load.Diags.Error())
			}
			result, diags := identity.ResolveWith(context.Background(), load.Config, identity.Context{
				ManagedResults: planCertResult(),
			})
			if len(load.UnsetVariables()) == 0 {
				t.Fatal("resolution never read a required root variable with no value, so this is not testing the substituted-unknown path at all")
			}
			if !diags.HasErrors() {
				t.Fatal("resolved a configuration reading a required root variable with no value; #183 rules it must stay refused")
			}
			for _, res := range result.All() {
				if res.Addr.Resource.Resource.Type == "aws_s3_bucket" {
					t.Errorf("%s was resolved anyway, as %v with import ID %q and cause %s",
						res.Addr, res.Class, res.ImportID, res.Cause)
				}
			}
		})
	}
}

// resolveFixture loads a fixture through THIS package's loader - the one that
// substitutes an unknown for an unset required variable - and resolves it with
// no results of any kind in hand, which is what a first pass has.
func resolveFixture(t *testing.T, dir string) (LoadResult, *identity.Result, tfdiags.Diagnostics) {
	t.Helper()
	load := Load(context.Background(), filepath.Join("testdata", dir))
	if load.Config == nil {
		t.Fatalf("fixture %s did not load: %s", dir, load.Diags.Error())
	}
	result, diags := identity.ResolveWith(context.Background(), load.Config, identity.Context{})
	return load, result, diags
}
