// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// Issue #187's for_each half. A for_each value decides two different things
// and only one of them becomes an address: which instances exist (the keys)
// and what each.value holds inside them. Stock OpenTofu separates the two -
// a map or object only has to be known ITSELF, a set has to be known through
// and through, because a set's elements ARE its keys. This package asked
// IsWhollyKnown of both and so refused a configuration stock plans.
//
// Each test below is paired with its opposite, because a rule that accepts
// more is only correct if something still refuses.

// planCertResult is the certificate as the AWS provider PLANS it, which is
// what projection.PlanInstances hands back: domain_validation_options is
// present with its domain_name filled in from domain_name and
// subject_alternative_names, and the DNS record the caller has to publish is
// unknown until apply. Distinct from certResult in managedresults_test.go,
// which is the same object after a READ and is wholly known.
func planCertResult() map[string]cty.Value {
	return map[string]cty.Value{
		"aws_acm_certificate.cert": cty.ObjectVal(map[string]cty.Value{
			"arn":         cty.UnknownVal(cty.String),
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

// TestForEachMapKeysKnownValuesUnknownExpands is the carrier. The
// acm-certificate module at the centre of #187 keys its validation records on
// the certificate's own domain names, which the provider fills in during
// PlanResourceChange with no cloud call, and puts the apply-time record name
// in the map VALUE. That is precisely the arrangement stock's own refusal text
// tells users to write ("define the map keys statically in your configuration
// and place apply-time results only in the map values"), so the instance set
// has to come out.
// The block expands, and the instances it expands to are classified rather
// than refused: their name and type come from a value the provider does not
// fill in until the certificate is applied, which is
// [DiscoverySiblingApply]. What this pins is both halves - the key set is no
// longer the thing being refused, AND each instance renders the empty import
// ID that says "nothing here knows what this object is called", never a
// plausible-looking string.
func TestForEachMapKeysKnownValuesUnknownExpands(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{ManagedResults: planCertResult()})
	if hasSummary(diags, "Non-static for_each expression") {
		t.Fatalf("the key set is still being refused: %s", diags.Err())
	}
	if diags.HasErrors() {
		t.Fatalf("refused: %s", diags.Err())
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
	want := []string{
		`aws_route53_record.cert_validation["example.com"] NEEDS_DISCOVERY "" cause=SIBLING_APPLY args=[aws_acm_certificate.cert name type]`,
	}
	if len(got) != len(want) {
		t.Fatalf("resolved %d route53 record instance(s):\n  %s\nwant %d:\n  %s",
			len(got), strings.Join(got, "\n  "), len(want), strings.Join(want, "\n  "))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("instance %d rendered\n  %s\nwant\n  %s", i, got[i], want[i])
		}
	}
}

// TestSiblingApplyNeedsTheManagedResult is the refusal half of the test
// above, and it is the one that says the classification is not simply "an
// unknown identity argument is fine now".
//
// The identical configuration, resolved with nothing in hand, must still
// refuse - at the key set, because without the certificate's planned value
// there is no key set either. A run that classified this would have
// reclassified every unset-variable configuration in the corpus along with
// it.
func TestSiblingApplyNeedsTheManagedResult(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{})
	if !diags.HasErrors() {
		t.Fatal("resolved a for_each over a computed sibling attribute with no managed results in hand")
	}
	if !hasSummary(diags, "Non-static for_each expression") {
		t.Fatalf("refused for some other reason: %s", diags.Err())
	}
	if result != nil {
		for _, res := range result.All() {
			if res.Addr.Resource.Resource.Type == "aws_route53_record" {
				t.Errorf("%s was classified anyway, as %v with import ID %q and cause %s",
					res.Addr, res.Class, res.ImportID, res.Cause)
			}
		}
	}
}

// TestSiblingApplyDoesNotSwallowAnUnrelatedRefusal is the fail-closed half.
//
// The same shape with a second identity argument that is broken for a reason
// no apply will settle - zone_id read from a required root variable this
// loader refuses outright - must stay a REFUSAL. The classification is
// all-or-nothing on purpose: a component failing for an unrelated reason
// standing beside a sibling-apply one would otherwise have its diagnostic
// withdrawn along with the others and vanish.
func TestSiblingApplyDoesNotSwallowAnUnrelatedRefusal(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach-broken-sibling"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{ManagedResults: planCertResult()})
	if !diags.HasErrors() {
		t.Fatal("a resource whose zone_id cannot be evaluated at all resolved anyway")
	}
	if result != nil {
		for _, res := range result.All() {
			if res.Addr.Resource.Resource.Type == "aws_route53_record" {
				t.Errorf("%s was classified as %v (cause %s) despite an unrelated component failing",
					res.Addr, res.Class, res.Cause)
			}
		}
	}
}

// TestSiblingApplyFromADirectReference is the discriminator's other leg: an
// identity argument that names the covered resource's attribute itself, with
// no for_each anywhere. It is the shape that needs no expansion to carry the
// provenance, and it is here because every other test in this file exercises
// the each-carried leg only.
//
// # The fallback that keeps this as good as the no-results answer
//
// Resolved with NOTHING in hand this fixture's log group comes back
// PARENT_DERIVED, carrying the formula ${aws_acm_certificate.cert.arn} - a
// better answer than a NEEDS_DISCOVERY/SIBLING_APPLY classification, because
// marker discovery can render the formula once the certificate is found.
// [resolver.managedCovered] used to cost this run that answer the moment it
// held results for the certificate: the reference stopped being symbolic, so
// [resolver.resolveExpr] evaluated it directly, got an unknown .arn, and
// classified it instead of building the formula.
//
// [resolver.resolveExpr] now retries a direct managed-attribute reference
// through [resolver.resolveTraversal] - the same route isSymbolic would have
// taken - whenever the evaluated value comes back unknown, so this fixture
// renders the identical formula whether or not this run holds a plan for the
// certificate. See internal/live/check/testdata/identity-golden.txt, where
// that formula is the line this fixture contributes.
func TestSiblingApplyFromADirectReference(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-direct-arg"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: map[string]cty.Value{
			"aws_acm_certificate.cert": cty.ObjectVal(map[string]cty.Value{
				"arn":         cty.UnknownVal(cty.String),
				"domain_name": cty.StringVal("example.com"),
			}),
		},
	})
	if diags.HasErrors() {
		t.Fatalf("refused: %s", diags.Err())
	}
	res := resolutionAt(t, result, "aws_cloudwatch_log_group.app")
	const want = `aws_cloudwatch_log_group.app PARENT_DERIVED ${aws_acm_certificate.cert.arn}`
	if got := res.String(); got != want {
		t.Errorf("rendered\n  %s\nwant\n  %s", got, want)
	}
	if res.Cause != "" || len(res.CauseArgs) != 0 {
		t.Errorf("a formula answer should carry no discovery cause; got cause=%s args=%v", res.Cause, res.CauseArgs)
	}
}

// TestSiblingApplyNotClaimedForAKnownSibling is that leg's refusal half: the
// covered resource's value is wholly KNOWN, and the unknown in the same
// argument comes from a data source instead.
//
// Attributing it to the certificate would tell an operator to apply something
// that is already applied. The rule asks whether the covered VALUE is unknown,
// not whether the expression happens to mention a covered resource, and this
// is where that distinction is pinned.
//
// The child is aws_iam_group, not aws_cloudwatch_log_group as this fixture
// read before GitHub issue #289: that type's own marker fallback would now
// answer the data-source-unknown refusal below too, which would make this
// test pass for the wrong reason - the instance not resolving at all,
// rather than the instance not being wrongly attributed to the certificate.
// aws_iam_group has no tags argument and stays outside that gate.
func TestSiblingApplyNotClaimedForAKnownSibling(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-known-plus-data"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: map[string]cty.Value{
			"aws_acm_certificate.cert": cty.ObjectVal(map[string]cty.Value{
				"domain_name": cty.StringVal("example.com"),
			}),
		},
		DataResults: map[string]cty.Value{
			"data.aws_region.current": cty.ObjectVal(map[string]cty.Value{
				"name": cty.UnknownVal(cty.String),
			}),
		},
	})
	if !diags.HasErrors() {
		t.Fatal("an identity argument holding an unknown data-source value resolved anyway")
	}
	if result != nil {
		for _, res := range result.All() {
			if res.Addr.Resource.Resource.Type == "aws_iam_group" {
				t.Errorf("%s was classified as %v (cause %s, import ID %q); the certificate it names is wholly known and is not what this run is waiting on",
					res.Addr, res.Class, res.Cause, res.ImportID)
			}
		}
	}
}

// TestSiblingApplyNotClaimedForANonEachArgument is the each-carried leg's
// refusal half. The block's for_each IS managed-derived, so the expansion
// carries the provenance, and the argument that fails reads a data source
// rather than each.*.
//
// Without the rule that the each-carried provenance applies only to an
// argument reading each.*, this instance would be reported as waiting on a
// certificate that has nothing to do with its missing zone.
func TestSiblingApplyNotClaimedForANonEachArgument(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach-data-arg"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: planCertResult(),
		DataResults: map[string]cty.Value{
			"data.aws_route53_zone.main": cty.ObjectVal(map[string]cty.Value{
				"zone_id": cty.UnknownVal(cty.String),
			}),
		},
	})
	if !diags.HasErrors() {
		t.Fatal("an identity argument holding an unknown data-source value resolved anyway")
	}
	if result != nil {
		for _, res := range result.All() {
			if res.Addr.Resource.Resource.Type == "aws_route53_record" {
				t.Errorf("%s was classified as %v (cause %s, import ID %q); its zone_id came from a data source, not from the certificate",
					res.Addr, res.Class, res.Cause, res.ImportID)
			}
		}
	}
}

// TestSiblingApplyIsNotReachedThroughAVariable is the #183 guard expressed in
// this package: an identity argument that reads a root variable is never
// attributed to the managed read, even when the block's for_each genuinely
// was built from one.
//
// It matters because internal/live/check's loader substitutes an unknown for
// an unset required variable, so under THAT loader `name = var.record_name`
// arrives at exactly the branch this classification lives in. The rule
// refuses to attribute anything whose expression names a variable, and this
// is where that is pinned.
func TestSiblingApplyIsNotReachedThroughAVariable(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach-var-arg"), map[string]cty.Value{
		"record_name": cty.UnknownVal(cty.String),
	})

	result, diags := ResolveWith(context.Background(), cfg, Context{ManagedResults: planCertResult()})
	if !diags.HasErrors() {
		t.Fatal("an identity argument reading a variable with no known value resolved anyway")
	}
	if !hasSummary(diags, "Non-static identity argument") {
		t.Fatalf("refused for some other reason: %s", diags.Err())
	}
	if result != nil {
		for _, res := range result.All() {
			if res.Addr.Resource.Resource.Type == "aws_route53_record" {
				t.Errorf("%s was classified as %v with cause %s; an unknown reached through a variable must never be attributed to a managed read",
					res.Addr, res.Class, res.Cause)
			}
		}
	}
}

// TestForEachMapKeysKnownValuesUnknownRefusedWithoutTheFix is the refusal
// proof for the test above, expressed against the value rather than the
// resolver so it cannot pass by accident: the same map, asked the old
// question.
func TestForEachMapKeysKnownValuesUnknownRefusedWithoutTheFix(t *testing.T) {
	val := cty.ObjectVal(map[string]cty.Value{
		"example.com": cty.ObjectVal(map[string]cty.Value{
			"name": cty.UnknownVal(cty.String),
			"type": cty.UnknownVal(cty.String),
		}),
	})
	if val.IsWhollyKnown() {
		t.Fatal("the fixture value is wholly known, so it is not testing the case")
	}
	if !forEachKeysKnown(val) {
		t.Error("a map with known keys and unknown values does not determine its own keys; stock OpenTofu says it does")
	}
}

// TestForEachUnsetVariableStillRefuses is the #183 guard: the cohort
// live/corpus-manifest.json rules must stay blocked keys its for_each on a
// required root variable with no value, and widening the map rule must not
// reach it.
//
// It reaches it by a wider margin than the rule's own narrowing provides, and
// that is worth pinning rather than assuming. An unset required root variable
// does not evaluate to an unknown at all - [configs.StaticEvaluator] refuses
// it outright, with "No value for required variable" - so the for_each value
// never becomes a value this rule is asked about. The unknowns
// forEachKeysKnown does see come from somewhere a caller put them, which today
// is only [Context.ManagedResults] and [Context.DataResults].
//
// # What this test does NOT cover, and nobody should read into it
//
// The paragraph above is true of THIS package's loader, which is the one this
// test uses. It is false of [internal/live/check], and check is the path
// tools/refusal-probe, "just corpus" and live-check all take: check/load.go
// substitutes cty.UnknownVal(variable.ConstraintType) for an unset required
// variable rather than refusing it.
//
// Measured on this same fixture: identity's own loader answers "No value for
// required variable", and check.Dir answers "Non-static for_each expression"
// with zero instances. Both refuse, so #183's cohort is safe today - but they
// refuse for DIFFERENT reasons, and only one of them is the reason written
// above.
//
// That matters for the work #187 still needs. A rule that classifies an
// unknown identity argument instead of refusing it has to tell a
// managed-read unknown from an unset-variable unknown, and under check both
// arrive as the same cty.UnknownVal. Passing this test proves nothing about
// that case. The guard for it belongs in the check package, against the
// substituted value, and does not exist yet.
func TestForEachUnsetVariableStillRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-unset-var-map"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{})
	if !diags.HasErrors() {
		t.Fatal("resolved a for_each over a required variable with no value")
	}
	if !hasSummary(diags, "No value for required variable") {
		t.Fatalf("refused for some other reason: %s", diags.Err())
	}
	if result != nil {
		for _, res := range result.All() {
			if res.Addr.Resource.Resource.Type == "aws_s3_bucket" {
				t.Errorf("%s expanded anyway; nothing here can say which instances exist", res.Addr)
			}
		}
	}
}

// TestForEachSetWithUnknownElementStillRefuses is the other half of the
// narrowing. A set's elements are its keys, so an unknown element is an
// unknown address; stock refuses this and so must this package, even with the
// provider's planned value in hand.
func TestForEachSetWithUnknownElementStillRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-unknown-values-set"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{ManagedResults: planCertResult()})
	if !diags.HasErrors() {
		t.Fatal("resolved a for_each over a set whose one element is unknown until apply")
	}
	if !hasSummary(diags, "Non-static for_each expression") {
		t.Fatalf("refused for some other reason: %s", diags.Err())
	}
}
