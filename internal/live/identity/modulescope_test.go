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
)

// TestModuleCallProviderRemapSurvivesAVarRead is the regression for a
// restore that put back three of the four fields [resolver.enterModuleAt]
// sets. Reading `var.name` inside a child module climbs to the parent to
// evaluate the module call's own argument, and the hop back down restored
// mod, modInst and eval but not curCfg - so every resource whose identity
// reads a var inside a module had its provider configuration resolved
// against the module's PARENT.
//
// The visible consequence is here: two calls of one module, remapped to two
// aliased provider configurations in two regions, both declaring the same
// name. With curCfg left at the parent, both resolved to the root's default
// aws provider, their cloud scopes matched, and [resolver.checkCollisions]
// reported one identity for two live objects - refusing a configuration
// that works, which is the direction that costs a user their run.
//
// It asserts the scopes rather than only the absence of the diagnostic,
// because the diagnostic can also be absent for reasons that have nothing
// to do with this (the resources failing to resolve at all, say).
func TestModuleCallProviderRemapSurvivesAVarRead(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "module-provider-remap"), nil)

	result, diags := ResolveWith(context.Background(), cfg, Context{Schemas: recordBackedTestSchemas()})
	for _, d := range diags {
		if d.Severity() == 1 { // tfdiags.Error
			t.Errorf("unexpected error: %s: %s", d.Description().Summary, d.Description().Detail)
		}
	}

	want := map[string]string{
		"module.west.aws_cloudwatch_log_group.this": `provider["registry.opentofu.org/hashicorp/aws"]`,
		"module.east.aws_cloudwatch_log_group.this": `provider["registry.opentofu.org/hashicorp/aws"].east`,
	}
	wantRegion := map[string]string{
		"module.west.aws_cloudwatch_log_group.this": "eu-west-1",
		"module.east.aws_cloudwatch_log_group.this": "us-east-1",
	}
	seen := map[string]bool{}
	for _, res := range result.All() {
		addr := res.Addr.String()
		base, ok := want[addr]
		if !ok {
			continue
		}
		seen[addr] = true
		if res.cloudScope.base != base {
			t.Errorf("%s resolved provider configuration %q, want %q.\n"+
				"The module call's own providers mapping was lost, which makes two regions look like one.",
				addr, res.cloudScope.base, base)
		}
		if !res.cloudScope.regionKnown || res.cloudScope.region != wantRegion[addr] {
			t.Errorf("%s resolved region %q (known=%v), want %q",
				addr, res.cloudScope.region, res.cloudScope.regionKnown, wantRegion[addr])
		}
	}
	for addr := range want {
		if !seen[addr] {
			t.Errorf("%s did not resolve at all; this test now proves nothing about it", addr)
		}
	}

	for _, d := range diags {
		if strings.Contains(d.Description().Summary, "same identity") {
			t.Errorf("two resources in two regions were reported as one identity: %s", d.Description().Detail)
		}
	}
}
