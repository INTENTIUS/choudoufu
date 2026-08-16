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

// A Component.SoleElement argument reached through a variable, not a
// syntactic list construct: [resolver.soleElementFromValue]'s whole point.
// One element must narrow and resolve; more than one must still refuse
// "Ambiguous list-valued identity argument", never "Non-string identity
// argument" - the collection must never reach stringValue whole.
func TestSoleElementFromValue(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "sole-element-from-value"), nil)

	result, diags := Resolve(context.Background(), cfg)

	res := resolutionAt(t, result, "aws_security_group_rule.from_default")
	if res.Class != ClassConcrete {
		t.Fatalf("from_default resolved %s; every value it needs is a literal or a defaulted variable", res.Class)
	}
	want := "sg-0123456789abcdef0_ingress_tcp_22_22_10.0.0.0/8"
	if res.ImportID != want {
		t.Errorf("from_default resolved to %q, want %q", res.ImportID, want)
	}

	if _, ok := result.Get(mustAddr(t, "aws_security_group_rule.ambiguous")); ok {
		t.Fatalf("aws_security_group_rule.ambiguous resolved; var.many has two elements, and the AWS API - not list order - decides how they compose")
	}
	foundAmbiguous := false
	foundNonString := false
	for _, d := range diags {
		switch d.Description().Summary {
		case "Ambiguous list-valued identity argument":
			foundAmbiguous = true
		case "Non-string identity argument":
			foundNonString = true
		}
	}
	if !foundAmbiguous {
		t.Fatalf("expected an %q diagnostic, got: %v", "Ambiguous list-valued identity argument", diags)
	}
	if foundNonString {
		t.Fatalf("var.many should refuse as an ambiguous collection, not reach stringValue as a whole non-string value: %v", diags)
	}
}
