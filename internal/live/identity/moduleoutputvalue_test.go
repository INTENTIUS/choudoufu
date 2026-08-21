// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

// TestModuleOutputInsideModuleCallArgument is the resolving half of
// [resolver.moduleOutputValue].
//
// The configuration is the shape terraform-aws-modules/terraform-aws-rds's
// complete-postgres example writes: a module call argument that is a literal
// list of objects, one of whose leaves reads another module call's output.
// [configs.ModuleCall.VariablesUsing] evaluates that argument as ONE
// expression, so before this the single module-output leaf refused the whole
// of var.rules_a inside the child and took the literal from_port/to_port
// with it - and stock OpenTofu plans the same configuration without
// complaint.
//
// The asserted value is the whole point: the CIDR is 10.77.0.0/16, written
// once in the root module's locals and reachable only by evaluating the
// child module's own output expression with the caller's argument bound.
// A fabricated or defaulted value would not spell it.
func TestModuleOutputInsideModuleCallArgument(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-in-call-arg"), nil)

	result, _ := ResolveWith(context.Background(), cfg, Context{Schemas: fakeProviderSchemas(map[string]fakeType{
		"aws_vpc": {
			args:     map[string]string{"id": "optcomp", "cidr_block": "optcomp", "plain_cidr": "opt", "tags": "opt"},
			identity: map[string]string{"id": "req"},
		},
		"aws_security_group_rule": {
			args: map[string]string{
				"id": "optcomp", "security_group_id": "req", "type": "req",
				"protocol": "req", "from_port": "req", "to_port": "req",
				"cidr_blocks": "opt",
			},
		},
	})})

	res := resolutionAt(t, result, "module.sg.aws_security_group_rule.a[0]")
	if res.Class != ClassConcrete {
		t.Fatalf("resolved %s, want CONCRETE", res.Class)
	}
	const want = "sg-fixed_ingress_tcp_5432_5432_10.77.0.0/16"
	if res.ImportID != want {
		t.Errorf("ImportID = %q, want %q", res.ImportID, want)
	}
}

// TestModuleOutputInsideModuleCallArgumentRefuses is the adversarial half,
// and the one that decides whether this is safe to have.
//
// Every case here names a module output whose value this package must NOT
// substitute into the rebuilt argument, and each must leave the rule
// unresolved rather than resolved to something plausible. A fix that turns a
// refusal into a wrong marker is worse than the refusal, so these assert
// "did not resolve" - never merely "a diagnostic exists somewhere".
func TestModuleOutputInsideModuleCallArgumentRefuses(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-output-in-call-arg"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: fakeProviderSchemas(map[string]fakeType{
		"aws_vpc": {
			args:     map[string]string{"id": "optcomp", "cidr_block": "optcomp", "plain_cidr": "opt", "tags": "opt"},
			identity: map[string]string{"id": "req"},
		},
		"aws_security_group_rule": {
			args: map[string]string{
				"id": "optcomp", "security_group_id": "req", "type": "req",
				"protocol": "req", "from_port": "req", "to_port": "req",
				"cidr_blocks": "opt",
			},
		},
	})})

	resolved := map[string]string{}
	if result != nil {
		for _, r := range result.All() {
			resolved[r.Addr.String()] = r.ImportID
		}
	}

	for _, tc := range []struct{ addr, why string }{
		{"module.sg.aws_security_group_rule.b[0]",
			"the output reads a managed resource's Optional+Computed attribute; the provider has its own path to a different value"},
		{"module.sg.aws_security_group_rule.c[0]",
			"the output reads a managed resource attribute, which the child module's own static evaluator cannot answer"},
		{"module.sg.aws_security_group_rule.d[0]",
			"the output calls uuid(), so its value differs on every run"},
		{"module.sg.aws_security_group_rule.e[0]",
			"the output is declared sensitive and a marker is written to a cloud tag in clear"},
		{"module.sg.aws_security_group_rule.f[0]",
			"the output carries two CIDRs and the AWS API, not this list's order, decides how they compose"},
	} {
		if id, ok := resolved[tc.addr]; ok {
			t.Errorf("%s resolved to %q but must refuse: %s", tc.addr, id, tc.why)
		}
	}

	// The one refusal whose WORDING is load-bearing: a two-element
	// collection has to say so, not fail as an unavailable variable, so a
	// reader is told which of the two facts stopped it.
	var sawAmbiguous bool
	for _, d := range diags {
		if d.Description().Summary == "Ambiguous list-valued identity argument" &&
			strings.Contains(d.Description().Detail, "aws_security_group_rule.f") {
			sawAmbiguous = true
		}
	}
	if !sawAmbiguous {
		t.Errorf("the two-element output refused without saying it had two elements")
	}
}
