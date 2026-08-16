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

// An identity argument reading a child module's output used to refuse with
// "Module output not supported in static context", raised by
// [configs.StaticValidateReferences]. That refusal is right for the
// contexts it was written for - a module source, a backend, an encryption
// block, all of which are resolved before the module tree exists - and
// wrong for an identity argument, which is evaluated with the whole
// configuration already loaded. Stock OpenTofu evaluates every reference in
// this fixture without complaint, so refusing them was a parity defect
// rather than a matched limitation.
//
// See [resolver.resolveModuleOutput].
func TestModuleOutputResolves(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output"), nil)

	result, diags := Resolve(context.Background(), cfg)
	for _, d := range diags {
		if d.Description().Summary == "Module output not supported in static context" {
			t.Errorf("still refused: %s", d.Description().Detail)
		}
	}

	for _, tc := range []struct {
		addr string
		want string
	}{
		// The output is a template over the call's own argument.
		{"aws_s3_bucket_policy.direct", "app-assets-store"},
		// The output is a resource attribute inside the child module, so
		// the value comes back through the same parentPart path a direct
		// sibling reference would use.
		{"aws_s3_bucket_versioning.via_resource", "app-assets-store"},
		// The module hop is reached through a local, which means
		// selectStatic's chase carried it rather than namedLeaf.
		{"aws_iam_role_policy.via_local", "worker:inline"},
		// A key selected out of an output whose value is an object.
		{"aws_iam_role_policy.via_object_output", "worker:second"},
	} {
		res := resolutionAt(t, result, tc.addr)
		if res.Class != ClassConcrete {
			t.Errorf("%s resolved %s, want CONCRETE", tc.addr, res.Class)
			continue
		}
		if res.ImportID != tc.want {
			t.Errorf("%s resolved to %q, want %q", tc.addr, res.ImportID, tc.want)
		}
	}
}

// The adversarial half, and the one that matters: every shape here names
// something that is not one value, and the module-output walk must decline
// each rather than resolve it. A fix that turns a refusal into silence is
// worse than the refusal, so these are asserted as "did not resolve", not
// merely as "some diagnostic exists somewhere".
func TestModuleOutputRefusesWhatItCannotName(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-refused"), nil)

	result, _ := Resolve(context.Background(), cfg)

	resolved := map[string]bool{}
	if result != nil {
		for _, r := range result.All() {
			resolved[r.Addr.String()] = true
		}
	}

	for _, tc := range []struct {
		addr string
		why  string
	}{
		{
			"aws_iam_role_policy.whole_repeated",
			"module.shard.name on a for_each'd call is an object of every instance's name, not one name",
		},
		{
			"aws_iam_role_policy.whole_repeated_literal_output",
			"module.shard.constant is an object keyed by every instance, and its literal value hides that from every check downstream of the module hop",
		},
		{
			"aws_iam_role_policy.missing_key",
			`module.shard["c"] names an instance the call's for_each does not expand to`,
		},
		{
			"aws_iam_role_policy.missing_key_literal_output",
			`module.shard["c"] names no instance, and this output is a literal, so nothing downstream would catch it`,
		},
		{
			"aws_iam_role_policy.indexed_unrepeated",
			"module.single[0] indexes a call that has no count or for_each",
		},
		{
			"aws_iam_role_policy.no_such_output",
			"module.single.nonexistent names an output the child module does not declare",
		},
	} {
		if resolved[tc.addr] {
			t.Errorf("%s resolved; it must not, because %s", tc.addr, tc.why)
		}
	}
}

// A for_each'd module call, with each instance's output reached by its own
// key. The failure this guards against is not a refusal but a wrong answer:
// if the module hop resolved every key against one instance, or leaked the
// referring resource's scope across the boundary, both policies would come
// back naming the same role and nothing would complain. Asserting distinct
// values is what makes that visible.
func TestModuleOutputKeepsInstancesApart(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-foreach"), nil)

	result, _ := Resolve(context.Background(), cfg)

	blue := resolutionAt(t, result, "aws_iam_role_policy.blue")
	green := resolutionAt(t, result, "aws_iam_role_policy.green")

	if blue.ImportID != "b-role:p" {
		t.Errorf("blue resolved to %q, want %q", blue.ImportID, "b-role:p")
	}
	if green.ImportID != "g-role:p" {
		t.Errorf("green resolved to %q, want %q", green.ImportID, "g-role:p")
	}
	if blue.ImportID == green.ImportID {
		t.Fatalf("both policies resolved to %q; the module hop is not keeping module.shard's instances apart", blue.ImportID)
	}
}
