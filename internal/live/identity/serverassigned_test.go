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

// A resource that omits an identity argument the provider's own docs
// document as auto-generated when absent - "If omitted, Terraform will
// assign a random, unique name" and its siblings (#190) - is not a missing
// identity argument the way an ordinary omission is: the provider fills it
// in at create time, so the instance defers to discovery instead of
// refusing. [Component.ServerAssignedIfAbsent] is what tells this apart from
// a plain missing argument, the same way [firstPrefixSibling] does for the
// "<name>_prefix" convention (nameprefix_test.go) - a different spelling of
// the identical situation. A sibling instance that sets the argument in the
// same configuration must keep resolving concrete: the rule is per-instance,
// not per-type.
func TestServerAssignedIfAbsentDefersToDiscovery(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "server-assigned-if-absent"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for _, addr := range []string{"aws_iam_role_policy.omitted", "aws_lambda_permission.omitted"} {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassNeedsDiscovery {
			t.Fatalf("%s resolved %s; an omitted server-assigned-if-absent argument should be NEEDS_DISCOVERY", addr, res.Class)
		}
	}

	named := resolutionAt(t, result, "aws_iam_role_policy.named")
	if named.Class != ClassConcrete {
		t.Fatalf("named resolved %s; it sets the plain name argument and should stay CONCRETE regardless of its omitted sibling", named.Class)
	}
	if want := "example-role:explicit-policy-name"; named.ImportID != want {
		t.Errorf("named resolved to %q, want %q", named.ImportID, want)
	}
}
