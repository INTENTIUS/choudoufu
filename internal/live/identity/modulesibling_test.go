// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"testing"
)

// TestWalkModuleRestoresContextBetweenSiblings covers a walkModule bug found
// while chasing issue #200: a module call whose own children get walked
// first (alphabetically) left the resolver's r.mod/r.modInst/r.curCfg
// pointing deep into that subtree, with nothing restoring them before the
// NEXT sibling's own count/for_each lookup ran against r.mod.ModuleCalls.
// The next sibling's module call was then invisible to that lookup, so its
// count was never read and it was walked as a single unkeyed instance
// regardless of what count actually said - govuk-infrastructure's
// opensearch blue/green module hit this exactly: "blue_domain" (which has
// its own nested module) sorts before "snapshot_bucket" (count = 0), and
// snapshot_bucket was walked as a phantom instance every time.
func TestWalkModuleRestoresContextBetweenSiblings(t *testing.T) {
	cfg := loadConfigTree(t, "testdata/module-sibling-count", nil)
	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	if _, ok := result.Get(mustAddr(t, "module.a_with_children.module.nested.aws_cloudwatch_log_group.leaf")); !ok {
		t.Errorf("module.a_with_children.module.nested.aws_cloudwatch_log_group.leaf did not resolve")
	}

	if _, ok := result.Get(mustAddr(t, "module.b_leaf_count.aws_cloudwatch_log_group.b")); ok {
		t.Errorf("module.b_leaf_count.aws_cloudwatch_log_group.b[0] resolved, but b_leaf_count's count is 0: " +
			"its own module call was walked as a phantom single instance instead of zero instances")
	}
}
