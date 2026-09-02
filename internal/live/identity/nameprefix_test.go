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

// A resource named through the "<name>_prefix" convention rather than
// "<name>" itself: #190's dominant bucket (aws_db_parameter_group,
// aws_launch_configuration, aws_iam_role and more all offer this pair).
// [resolver.firstPrefixSibling] is what tells this apart from a plain
// missing argument - the provider assigns the actual name at create time,
// so the instance defers to discovery instead of refusing outright. A
// sibling instance that sets the plain name in the same configuration must
// keep resolving concrete: the rule is per-instance, not per-type.
func TestNamePrefixDefersToDiscovery(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "name-prefix-discovery"), nil)

	result, diags := Resolve(context.Background(), cfg)
	assertNoErrors(t, diags)

	for _, addr := range []string{"aws_db_parameter_group.prefixed", "aws_iam_role.prefixed"} {
		res := resolutionAt(t, result, addr)
		if res.Class != ClassNeedsDiscovery {
			t.Fatalf("%s resolved %s; name_prefix defers the actual name to create time, so it should be NEEDS_DISCOVERY", addr, res.Class)
		}
	}

	named := resolutionAt(t, result, "aws_db_parameter_group.named")
	if named.Class != ClassConcrete {
		t.Fatalf("named resolved %s; it sets the plain name argument and should stay CONCRETE regardless of its prefixed sibling", named.Class)
	}
	if named.ImportID != "engine-params" {
		t.Errorf("named resolved to %q, want %q", named.ImportID, "engine-params")
	}
}
