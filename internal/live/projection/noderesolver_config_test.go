// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
)

// noSourceCreateFixture is a real HCL configuration - not a hand-built
// [NodeResolver] struct literal the way TestNodeResolver_NoSourceDefaultRefuses
// and TestNodeResolver_NoSourceCreateToggle above use - naming an aws_route
// whose only present argument (route_table_id) cannot derive an identity on
// its own (GitHub issue #365 ruling 4's no-source shape). noSourceCreate is
// the literal spelling written into `strict { no_source_create = ... }`, or
// "" to omit the argument entirely (the default).
func noSourceCreateFixture(t *testing.T, noSourceCreate string) string {
	t.Helper()
	strictBlock := ""
	if noSourceCreate != "" {
		strictBlock = `
    strict {
      no_source_create = "` + noSourceCreate + `"
    }`
	}
	return `
terraform {
  live {
    estate = "test-estate"` + strictBlock + `
  }
}

resource "aws_route" "r" {
  route_table_id = "rtb-0123456789abcdef0"
}
`
}

// TestNodeResolverNoSourceCreateWiredFromConfig is GitHub issue #365's own
// audit criterion - "a fixture proving it refuses exactly what it names" -
// read literally for ruling 4's toggle: a real `strict { no_source_create =
// ... }` block, decoded by [identity.NoSourceCreateFor] the way
// internal/command/live_mode.go and live_plan.go actually wire it
// (`r.resolver.NoSourceCreate = strict.CreatesFromNoSource(identity.NoSourceCreateFor(config))`),
// then handed to [NodeResolver.ResolveResourceIdentity].
//
// TestNodeResolver_NoSourceDefaultRefuses and
// TestNodeResolver_NoSourceCreateToggle already pin the resolver's own
// behavior once NoSourceCreate is set; what they do not exercise is the
// config-to-toggle wiring itself, which lives in
// internal/live/identity.NoSourceCreateFor and is otherwise proven only
// against hand-built *configs.Config values
// (internal/live/identity/secrets_test.go's TestNoSourceCreateFor). This
// closes that gap with an actual .tf fixture and the mutation check the
// task named: the identical configuration, minus the toggle, resolves
// (refuses) under the default; set to "create", the same no-source
// instance is admitted through - the toggle is provably the obstacle and
// nothing else about the fixture is.
//
// no_source_create has no live/e2e/limits fixture the way strict-secrets-
// refusal does, and that is a structural fact about the limits wing, not
// an omission: TestLimitsEnforced only ever calls lint.CheckContext, and
// this toggle's refusal fires in the plan-node seam
// (internal/live/projection.NodeResolver), never in lint - see
// internal/live/lint/testdata's "strict no_source_create, the toggle
// turned on" case, whose own comment says so. This test lives at the
// layer where the behavior actually is.
func TestNodeResolverNoSourceCreateWiredFromConfig(t *testing.T) {
	addr := locatedTestAddr(t, "aws_route", "r")
	val := cty.ObjectVal(map[string]cty.Value{
		"route_table_id":              cty.StringVal("rtb-0123456789abcdef0"),
		"destination_cidr_block":      cty.NullVal(cty.String),
		"destination_ipv6_cidr_block": cty.NullVal(cty.String),
		"destination_prefix_list_id":  cty.NullVal(cty.String),
	})

	writeFixture := func(t *testing.T, noSourceCreate string) *configs.Config {
		t.Helper()
		dir := t.TempDir()
		src := noSourceCreateFixture(t, noSourceCreate)
		if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte(src), 0o600); err != nil {
			t.Fatalf("writing fixture: %s", err)
		}
		return loadConfig(t, dir)
	}

	t.Run("default refuses", func(t *testing.T) {
		cfg := writeFixture(t, "")
		resolver := &NodeResolver{
			NoSourceCreate: strict.CreatesFromNoSource(identity.NoSourceCreateFor(cfg)),
		}
		if resolver.NoSourceCreate {
			t.Fatalf("an omitted no_source_create argument wired NoSourceCreate=true, want false (the default is refuse)")
		}

		target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, val, providers.Schema{})
		if found {
			t.Fatalf("expected found=false for a no-source instance under the default, got target=%#v", target)
		}
		if !diags.HasErrors() {
			t.Fatal("expected a refusal diagnostic for a no-source instance under the default no_source_create")
		}
		if !hasDiagSummary(diags, "No source for this instance's identity") {
			t.Errorf("expected the \"No source for this instance's identity\" summary, got:\n%s", diags.Err())
		}
	})

	t.Run("no_source_create = refuse, written out by hand, means the same thing", func(t *testing.T) {
		cfg := writeFixture(t, "refuse")
		resolver := &NodeResolver{
			NoSourceCreate: strict.CreatesFromNoSource(identity.NoSourceCreateFor(cfg)),
		}
		_, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, val, providers.Schema{})
		if found || !diags.HasErrors() {
			t.Fatalf("no_source_create = \"refuse\" written out by hand must refuse exactly like the omitted default: found=%v diags=%s", found, diags.Err())
		}
	})

	t.Run("no_source_create = create removes the obstacle", func(t *testing.T) {
		cfg := writeFixture(t, "create")
		resolver := &NodeResolver{
			NoSourceCreate: strict.CreatesFromNoSource(identity.NoSourceCreateFor(cfg)),
		}
		if !resolver.NoSourceCreate {
			t.Fatalf("no_source_create = \"create\" wired NoSourceCreate=false, want true")
		}

		target, found, diags := resolver.ResolveResourceIdentity(context.Background(), addr, val, providers.Schema{})
		if diags.HasErrors() {
			t.Fatalf("the toggle must silence the refusal, got: %s", diags.Err())
		}
		if found {
			t.Fatalf("a no-source instance must never report found=true even with the toggle set; got %#v", target)
		}
	})
}
