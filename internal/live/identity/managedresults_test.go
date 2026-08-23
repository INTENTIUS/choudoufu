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

// Issue #187's resolution seam: [Context.ManagedResults], the counterpart of
// [Context.DataResults] for an attribute of a managed resource this estate
// already owns. Nothing in this package reads anything; a caller that has
// discovered the resource and read it hands the value in, exactly as the
// data-read phase does.
//
// Each test below is paired with its own refusal proof, because an
// assertion that only ever sees the fix cannot tell a working seam from a
// configuration that never needed one.

func certResult() map[string]cty.Value {
	return map[string]cty.Value{
		"aws_acm_certificate.cert": cty.ObjectVal(map[string]cty.Value{
			"arn":         cty.StringVal("arn:aws:acm:us-west-1:1:certificate/x"),
			"domain_name": cty.StringVal("example.com"),
			"domain_validation_options": cty.SetVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
				"domain_name":           cty.StringVal("example.com"),
				"resource_record_name":  cty.StringVal("_v.example.com."),
				"resource_record_type":  cty.StringVal("CNAME"),
				"resource_record_value": cty.StringVal("_w.acm-validations.aws."),
			})}),
		}),
	}
}

// TestManagedResultExpandsForEachOverComputedAttribute is the carrier: a
// for_each comprehension over an attribute the resource block never sets.
// Without the read the whole block refuses; with it every instance resolves
// from its own arguments as any other block does.
func TestManagedResultExpandsForEachOverComputedAttribute(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{ManagedResults: certResult()})
	assertNoErrors(t, diags)

	res := resolutionAt(t, result, `aws_route53_record.cert_validation["example.com"]`)
	if res.Class != ClassConcrete {
		t.Fatalf("resolved %s; with the certificate's read in hand every value the record needs is known", res.Class)
	}
	if want := "Z0423220__v.example.com._CNAME"; res.ImportID != want {
		t.Errorf("resolved to %q, want %q", res.ImportID, want)
	}
}

// TestManagedResultAbsentStillRefuses is the refusal proof for the test
// above. The seam must be the only reason that block resolved.
func TestManagedResultAbsentStillRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{})
	if !diags.HasErrors() {
		t.Fatal("resolved with no managed result in hand; the for_each iterates an attribute nothing in the configuration sets")
	}
	if !hasSummary(diags, "Non-static for_each expression") {
		t.Fatalf("refused for some other reason: %s", diags.Err())
	}
}

// TestManagedResultLeavesWholeResourceForEachAlone is the newly-refuses
// guard. `for_each = aws_subnet.this` takes its instance keys from the
// parent block's own expansion, which is configuration data and needs no
// read at all. A caller that happens to hand in a read of that same parent
// must not divert it onto the evaluation path, where the parent's values
// are unknown and the whole block would start refusing.
func TestManagedResultLeavesWholeResourceForEachAlone(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-bare"), nil)

	subnets := map[string]cty.Value{
		`aws_subnet.this["a"]`: cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("subnet-a")}),
		`aws_subnet.this["b"]`: cty.ObjectVal(map[string]cty.Value{"id": cty.StringVal("subnet-b")}),
	}

	shape := func(rctx Context) map[string]string {
		result, diags := ResolveWith(context.Background(), cfg, rctx)
		assertNoErrors(t, diags)
		got := map[string]string{}
		for _, res := range result.All() {
			got[res.Addr.String()] = string(res.Class) + " " + res.ImportID
		}
		return got
	}

	without := shape(Context{})
	with := shape(Context{ManagedResults: subnets})
	for _, key := range []string{"a", "b"} {
		addr := `aws_route_table_association.this["` + key + `"]`
		if _, ok := without[addr]; !ok {
			t.Fatalf("%s did not resolve even without a read; the fixture is not testing what it claims", addr)
		}
		if with[addr] != without[addr] {
			t.Errorf("%s resolved %q with a read of the parent and %q without; the keys come from the parent's expansion either way", addr, with[addr], without[addr])
		}
	}
}

// TestManagedResultRejectsADataAddress holds the two seams apart. A data
// address handed in as a managed result is calling-code error, and it has
// to be said so rather than dropped - a dropped result resurfaces later as
// the generic dynamic-value refusal pointing the user at their own file.
func TestManagedResultRejectsADataAddress(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "managed-read-foreach"), nil)

	_, diags := ResolveWith(context.Background(), cfg, Context{
		ManagedResults: map[string]cty.Value{
			"data.aws_route53_zone.primary": cty.ObjectVal(map[string]cty.Value{"zone_id": cty.StringVal("Z1")}),
		},
	})
	if !hasSummary(diags, SummaryUnusableDataResult) {
		t.Fatalf("a data address in ManagedResults was accepted or dropped; got: %s", diags.Err())
	}
}

func hasSummary(diags interface{ Err() error }, summary string) bool {
	return diags.Err() != nil && strings.Contains(diags.Err().Error(), summary)
}
