// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/lang/marks"
)

// statelessProviders caches the value each provider configuration was
// configured with, and that value comes from StaticEvaluator.DecodeBlock
// (providerConfigValue). DecodeBlock, unlike its DecodeExpression sibling,
// has no guard refusing a sensitive value, and internal/configs/static_scope.go
// marks a `sensitive = true` input variable on the way in - so
//
//	variable "region" { sensitive = true }
//	provider "aws"    { region = var.region }
//
// caches a MARKED value, and cty.Value.AsString panics rather than errors on
// one.
//
// It is the same defect as the two projection call sites fixed alongside this
// (a static-evaluator-decoded value used without a mark guard) with a worse
// failure mode - a crash instead of a silent omission - and it sits in
// internal/command, which internal/live/marksafe's sweep does not reach:
// marksafe_test.go loads "./internal/live/..." and nothing else.

func markedRegionProviders(t *testing.T, val cty.Value) (*statelessProviders, addrs.AbsProviderConfig) {
	t.Helper()
	// No region from the environment, so the answer is about the cached value
	// and not about the machine the test runs on.
	t.Setenv("AWS_REGION", "")
	t.Setenv("AWS_DEFAULT_REGION", "")

	addr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
	p := &statelessProviders{configVals: map[string]cty.Value{providerCacheKey(addr): val}}
	return p, addr
}

// TestRegionOfASensitiveProviderConfigDoesNotPanic is the reproduction.
// Without the ContainsMarked guard this does not fail - it panics, taking
// live-plan down with it.
func TestRegionOfASensitiveProviderConfigDoesNotPanic(t *testing.T) {
	p, addr := markedRegionProviders(t, cty.ObjectVal(map[string]cty.Value{
		"region": cty.StringVal("us-east-1").Mark(marks.Sensitive),
	}))

	if got := p.region(addr); got != "" {
		t.Errorf("region = %q, want \"\": the region came from a sensitive variable, and this answer "+
			"becomes an operator-facing hint string. Falling through to the environment is what an unset "+
			"region already does; printing the secret is not", got)
	}
}

// TestRegionOfAnOrdinaryProviderConfigIsStillRead is the mutation check on
// the guard above: a guard that refused everything would pass that test and
// silently stop discovery from ever reaching the right region.
func TestRegionOfAnOrdinaryProviderConfigIsStillRead(t *testing.T) {
	p, addr := markedRegionProviders(t, cty.ObjectVal(map[string]cty.Value{
		"region": cty.StringVal("eu-west-2"),
	}))

	if got := p.region(addr); got != "eu-west-2" {
		t.Errorf("region = %q, want %q - the provider block states it and nothing about it is sensitive", got, "eu-west-2")
	}
}

// TestEndpointURLOfASensitiveProviderConfigDoesNotPanic covers the sibling
// accessor. Its guard is unreachable today for an unrelated reason - the
// lookup reads addr.String() while ConfiguredProvider writes
// providerCacheKey(addr), so it always misses the cache - which is why this
// test seeds BOTH keys: the panic is what is under test, not the key
// mismatch, and a test that only exercised the dead path would assert
// nothing.
func TestEndpointURLOfASensitiveProviderConfigDoesNotPanic(t *testing.T) {
	t.Setenv("AWS_ENDPOINT_URL_EC2", "")
	t.Setenv("AWS_ENDPOINT_URL", "")

	addr := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("aws")}
	val := cty.ObjectVal(map[string]cty.Value{
		"endpoints": cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
			"ec2": cty.StringVal("http://localhost:4566").Mark(marks.Sensitive),
		})}),
	})
	p := &statelessProviders{configVals: map[string]cty.Value{
		providerCacheKey(addr): val,
		addr.String():          val,
	}}

	if got := p.endpointURL(addr); got != "" {
		t.Errorf("endpointURL = %q, want \"\" for an endpoint derived from a sensitive variable", got)
	}
}
