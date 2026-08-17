// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
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
// The block expands: the diagnostics that remain are per-INSTANCE ones naming
// the expanded address, where before there was one refusal about the whole
// block's key set. Those per-instance refusals are the second half of #187 and
// are not what this test is about; what it pins is that the key set is no
// longer the thing being refused.
func TestForEachMapKeysKnownValuesUnknownExpands(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{ManagedResults: planCertResult()})
	if hasSummary(diags, "Non-static for_each expression") {
		t.Fatalf("the key set is still being refused: %s", diags.Err())
	}
	if diags.Err() == nil {
		t.Fatal("nothing refused at all; the fixture's record name is unknown until apply and its identity cannot be rendered from it")
	}
	// Named per instance, which is only possible if the instance exists.
	if !hasSummary(diags, `aws_route53_record.cert_validation["example.com"]`) {
		t.Errorf("no diagnostic names an expanded instance, so the block did not expand: %s", diags.Err())
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
