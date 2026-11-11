// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package tofu

import (
	"context"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/override"
	"github.com/intentius/choudoufu/internal/plans"
	"github.com/intentius/choudoufu/internal/plugins"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
)

// GitHub issue #2344 (#2256 U8): destroy-from-config provider overrides on
// the new runtime. Each test plans a destroy under the toggle, with the
// configuration setting a destroy-time argument the state holds a different
// value for, and checks that the delete's prior state (the plan's Before)
// carries the configured value. The provider is a mock with the argument in
// its schema, wrapped with the real overrides the way the CLI wraps every
// provider (internal/command/meta_providers.go). Without the port the new
// runtime plans the delete from the state as last applied.

// destroyFromConfigCase is one resource type: its configuration, the state
// it was last applied with, and the destroy-time arguments the delete must
// carry.
type destroyFromConfigCase struct {
	typeName string
	attrs    map[string]*configschema.Attribute
	config   string
	state    string
	want     map[string]cty.Value
}

func destroyFromConfigAttrs(extra map[string]cty.Type) map[string]*configschema.Attribute {
	attrs := map[string]*configschema.Attribute{
		"id": {Type: cty.String, Computed: true},
	}
	for name, ty := range extra {
		attrs[name] = &configschema.Attribute{Type: ty, Optional: true}
	}
	return attrs
}

var destroyFromConfigCases = map[string]destroyFromConfigCase{
	"aws_rds_cluster": {
		typeName: "aws_rds_cluster",
		attrs:    destroyFromConfigAttrs(map[string]cty.Type{"skip_final_snapshot": cty.Bool, "final_snapshot_identifier": cty.String}),
		config:   `skip_final_snapshot = true`,
		state:    `{"id":"c1","skip_final_snapshot":false,"final_snapshot_identifier":null}`,
		want:     map[string]cty.Value{"skip_final_snapshot": cty.True},
	},
	"aws_db_instance": {
		typeName: "aws_db_instance",
		attrs:    destroyFromConfigAttrs(map[string]cty.Type{"skip_final_snapshot": cty.Bool, "final_snapshot_identifier": cty.String, "delete_automated_backups": cty.Bool}),
		config: `skip_final_snapshot = true
  delete_automated_backups = true`,
		state: `{"id":"d1","skip_final_snapshot":false,"final_snapshot_identifier":null,"delete_automated_backups":false}`,
		want:  map[string]cty.Value{"skip_final_snapshot": cty.True, "delete_automated_backups": cty.True},
	},
	"aws_s3_bucket": {
		typeName: "aws_s3_bucket",
		attrs:    destroyFromConfigAttrs(map[string]cty.Type{"force_destroy": cty.Bool}),
		config:   `force_destroy = true`,
		state:    `{"id":"b1","force_destroy":false}`,
		want:     map[string]cty.Value{"force_destroy": cty.True},
	},
	"aws_iam_user": {
		typeName: "aws_iam_user",
		attrs:    destroyFromConfigAttrs(map[string]cty.Type{"force_destroy": cty.Bool}),
		config:   `force_destroy = true`,
		state:    `{"id":"u1","force_destroy":false}`,
		want:     map[string]cty.Value{"force_destroy": cty.True},
	},
	"aws_route53_zone": {
		typeName: "aws_route53_zone",
		attrs:    destroyFromConfigAttrs(map[string]cty.Type{"force_destroy": cty.Bool}),
		config:   `force_destroy = true`,
		state:    `{"id":"z1","force_destroy":false}`,
		want:     map[string]cty.Value{"force_destroy": cty.True},
	},
	"aws_ecr_repository": {
		typeName: "aws_ecr_repository",
		attrs:    destroyFromConfigAttrs(map[string]cty.Type{"force_delete": cty.Bool}),
		config:   `force_delete = true`,
		state:    `{"id":"r1","force_delete":false}`,
		want:     map[string]cty.Value{"force_delete": cty.True},
	},
}

// destroyFromConfigContext builds a context whose aws provider is a mock
// with c's schema, wrapped with every registered override.
func destroyFromConfigContext(t *testing.T, c destroyFromConfigCase) (*Context, *MockProvider) {
	t.Helper()
	p := &MockProvider{
		GetProviderSchemaResponse: getProviderSchemaResponseFromProviderSchema(&ProviderSchema{
			Provider:      &configschema.Block{},
			ResourceTypes: map[string]*configschema.Block{c.typeName: {Attributes: c.attrs}},
		}),
	}
	aws := addrs.NewDefaultProvider("aws")
	byType := override.ForProvider(aws, override.All())
	if len(byType[c.typeName]) == 0 {
		t.Fatalf("no override registered for %s", c.typeName)
	}
	ctx := testContext2(t, &ContextOpts{
		Plugins: plugins.NewLibrary(map[addrs.Provider]providers.Factory{
			aws: func() (providers.Interface, error) {
				return override.Wrap(p, byType), nil
			},
		}, nil),
	})
	return ctx, p
}

func destroyFromConfigFixture(t *testing.T, c destroyFromConfigCase) (*states.State, addrs.AbsResourceInstance) {
	t.Helper()
	addr := mustResourceInstanceAddr(c.typeName + ".this")
	state := states.BuildState(func(s *states.SyncState) {
		s.SetResourceInstanceCurrent(addr, &states.ResourceInstanceObjectSrc{
			AttrsJSON: []byte(c.state),
			Status:    states.ObjectReady,
		}, mustProviderConfig(`provider["registry.opentofu.org/hashicorp/aws"]`), addrs.NoKey)
	})
	return state, addr
}

// testDestroyFromConfigNewRuntime plans a destroy of one instance of c's
// type on the new runtime and checks the delete's prior state.
func testDestroyFromConfigNewRuntime(t *testing.T, c destroyFromConfigCase) {
	forceExperimentalRuntimeForTesting(t)

	m := testModuleInline(t, map[string]string{
		"main.tf": `
provider "aws" {}

resource "` + c.typeName + `" "this" {
  ` + c.config + `
}
`,
	})
	state, addr := destroyFromConfigFixture(t, c)
	ctx, p := destroyFromConfigContext(t, c)

	plan, diags := ctx.Plan(context.Background(), m, state, &PlanOpts{Mode: plans.DestroyMode})
	assertNoErrors(t, diags)

	rc := plan.Changes.ResourceInstance(addr)
	if rc == nil {
		t.Fatalf("no planned change for %s", addr)
	}
	if rc.Action != plans.Delete {
		t.Fatalf("%s planned %s, want %s", addr, rc.Action, plans.Delete)
	}
	schema := p.GetProviderSchemaResponse.ResourceTypes[c.typeName]
	change, err := rc.Decode(&schema)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range c.want {
		got := change.Before.GetAttr(name)
		if !got.RawEquals(want) {
			t.Errorf("%s: the delete's prior state has %s = %#v, want the configured %#v", addr, name, got, want)
		}
	}
}

func TestContextDestroyFromConfigNewRuntime_awsRdsCluster(t *testing.T) {
	testDestroyFromConfigNewRuntime(t, destroyFromConfigCases["aws_rds_cluster"])
}

func TestContextDestroyFromConfigNewRuntime_awsDbInstance(t *testing.T) {
	testDestroyFromConfigNewRuntime(t, destroyFromConfigCases["aws_db_instance"])
}

func TestContextDestroyFromConfigNewRuntime_awsS3Bucket(t *testing.T) {
	testDestroyFromConfigNewRuntime(t, destroyFromConfigCases["aws_s3_bucket"])
}

func TestContextDestroyFromConfigNewRuntime_awsIamUser(t *testing.T) {
	testDestroyFromConfigNewRuntime(t, destroyFromConfigCases["aws_iam_user"])
}

func TestContextDestroyFromConfigNewRuntime_awsRoute53Zone(t *testing.T) {
	testDestroyFromConfigNewRuntime(t, destroyFromConfigCases["aws_route53_zone"])
}

func TestContextDestroyFromConfigNewRuntime_awsEcrRepository(t *testing.T) {
	testDestroyFromConfigNewRuntime(t, destroyFromConfigCases["aws_ecr_repository"])
}

// TestContextDestroyFromConfigNewRuntime_applySendsConfigured covers the
// apply half: the new apply engine plans the delete again from the prior
// state, so the configured argument has to reach the provider's
// ApplyResourceChange from the plan, not only appear in the plan.
func TestContextDestroyFromConfigNewRuntime_applySendsConfigured(t *testing.T) {
	c := destroyFromConfigCases["aws_rds_cluster"]
	forceExperimentalRuntimeForTesting(t)

	m := testModuleInline(t, map[string]string{
		"main.tf": `
provider "aws" {}

resource "aws_rds_cluster" "this" {
  skip_final_snapshot = true
}
`,
	})
	state, addr := destroyFromConfigFixture(t, c)
	ctx, p := destroyFromConfigContext(t, c)

	plan, diags := ctx.Plan(context.Background(), m, state, &PlanOpts{Mode: plans.DestroyMode})
	assertNoErrors(t, diags)

	_, diags = ctx.Apply(context.Background(), plan, m, nil)
	assertNoErrors(t, diags)

	if !p.ApplyResourceChangeCalled {
		t.Fatalf("the provider was never asked to delete %s", addr)
	}
	prior := p.ApplyResourceChangeRequest.PriorState
	if got := prior.GetAttr("skip_final_snapshot"); !got.RawEquals(cty.True) {
		t.Errorf("%s: the delete was sent skip_final_snapshot = %#v, want the configured true", addr, got)
	}
}
