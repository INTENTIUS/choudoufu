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

// An empty for_each set with cty.DynamicPseudoType element type - a
// filtered for-comprehension that matches nothing - is parity with stock
// OpenTofu (internal/lang/evalchecks/eval_for_each.go's
// performSetTypeChecks), not the non-string-set shape "Invalid for_each
// set" exists for. It must expand to zero instances and raise no
// diagnostic.
func TestForEachEmptyDynamicSetIsNotInvalid(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "foreach-empty-dynamic-set"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	if got := len(result.All()); got != 0 {
		t.Fatalf("resolved %d instances, want 0 - the for_each set is empty", got)
	}
}
