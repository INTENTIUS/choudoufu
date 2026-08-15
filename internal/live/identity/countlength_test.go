// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"
)

// count = length(<resource ref>) is #178's count-length bucket: 11 sites
// across cyhy-amis, the only estate the corpus carries this shape on. The
// for_each analogue (forEachOverResource, resolve.go) already borrows a
// parent's own expansion for its keys; these tests pin the count analogue
// (countExpansion / rewriteResourceLength, resolve.go) to the same
// standard the parent-attr tests hold identity resolution to: the exact
// resolved identity, not merely the absence of a diagnostic, and a
// refusal proof for the shapes deliberately left unrecognized.

// TestCountFromLengthOfResource is the bare form: count = length(aws_eip.pool).
func TestCountFromLengthOfResource(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "count-length"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for i, want := range []string{"/estate/log-0", "/estate/log-1"} {
		res := resolutionAt(t, result, addrIndex("aws_cloudwatch_log_group.per_eip", i))
		if res.Class != ClassConcrete {
			t.Fatalf("instance %d resolved %s; every value it needs is in configuration", i, res.Class)
		}
		if res.ImportID != want {
			t.Errorf("instance %d resolved to %q, want %q", i, res.ImportID, want)
		}
	}

	// aws_eip.pool has count = 2, so a third instance of the dependent
	// resource must not exist: a wrong count invents or drops instances,
	// and #175's whole point is that a fix must not do that silently.
	if _, ok := result.Get(mustAddr(t, `aws_cloudwatch_log_group.per_eip[2]`)); ok {
		t.Fatalf("aws_cloudwatch_log_group.per_eip[2] resolved; aws_eip.pool only has 2 instances")
	}
}

// TestCountFromLengthGatedByTernary is count = <var> ? length(<resource>) :
// <literal> - mgmt_nessus_ec2.tf's and mgmt_bastion_ec2.tf's shape in
// cyhy-amis. Both branches are exercised: the flag true, taking the
// length(<resource>) arm, and the flag false, taking the literal 0 arm
// with no dependency on the resource at all.
func TestCountFromLengthGatedByTernary(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "count-length-gated"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for i := 0; i < 3; i++ {
		res := resolutionAt(t, result, addrIndex("aws_cloudwatch_log_group.gated", i))
		if res.Class != ClassConcrete {
			t.Fatalf("instance %d resolved %s; every value it needs is in configuration", i, res.Class)
		}
	}
	if _, ok := result.Get(mustAddr(t, `aws_cloudwatch_log_group.gated[3]`)); ok {
		t.Fatalf("aws_cloudwatch_log_group.gated[3] resolved; aws_eip.pool only has 3 instances")
	}

	cfgDisabled := loadConfig(t, filepath.Join("testdata", "count-length-gated"), map[string]cty.Value{
		"enabled": cty.False,
	})
	resultDisabled, diags := Resolve(context.Background(), cfgDisabled)
	assertNoErrors(t, diags)
	if _, ok := resultDisabled.Get(mustAddr(t, `aws_cloudwatch_log_group.gated[0]`)); ok {
		t.Fatalf("aws_cloudwatch_log_group.gated[0] resolved with var.enabled = false; count should be 0")
	}
}

// TestCountFromLengthBehindComparison is count = length(<resource>) > 0 ? 1
// : 0 - bod_docker_iam.tf's shape in cyhy-amis, where length(<resource>)
// feeds a comparison inside the ternary's condition rather than sitting in
// one of its branches.
func TestCountFromLengthBehindComparison(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "count-length-comparison"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	res := resolutionAt(t, result, `aws_cloudwatch_log_group.policy_gate[0]`)
	if res.Class != ClassConcrete {
		t.Fatalf("resolved %s; every value it needs is in configuration", res.Class)
	}
	if res.ImportID != "/estate/log" {
		t.Errorf("resolved to %q, want /estate/log", res.ImportID)
	}
}

// The negative proofs - length() of a for expression over a resource
// (#178's separate for/splat-into-a-list bucket), and length() of a
// resource with neither count nor for_each (a single object, whose
// length() is its attribute count rather than an instance count) - live in
// TestResolveErrors's table (identity_test.go, dirs count-length-unrecognized
// and count-length-singleton), which already asserts the exact diagnostic
// and that no instance was guessed into existence.

func addrIndex(base string, i int) string {
	return fmt.Sprintf("%s[%d]", base, i)
}
