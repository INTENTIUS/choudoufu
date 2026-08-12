// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/opentofu/opentofu/internal/addrs"
	"github.com/opentofu/opentofu/internal/configs"
	"github.com/opentofu/opentofu/internal/configs/configschema"
	"github.com/opentofu/opentofu/internal/providers"
	"github.com/opentofu/opentofu/internal/stateless/flocitest"
	"github.com/opentofu/opentofu/internal/stateless/identity"
	"github.com/opentofu/opentofu/internal/tfdiags"
	"github.com/opentofu/opentofu/internal/tofu"
)

// estateDir is the P0.1 fixture. The projection built from it with a fake
// cloud is the same shape the integration test builds from a real one.
func estateDir(t *testing.T) string {
	return flocitest.EstateDir(t)
}

// awsProvider is the provider configuration address every fixture in this
// package resolves to, since they all declare hashicorp/aws with no alias.
var awsProvider = addrs.AbsProviderConfig{
	Module:   addrs.RootModule,
	Provider: addrs.NewDefaultProvider("aws"),
}

// ---------------------------------------------------------------------------
// The estate, with a fake cloud standing in for floci.
// ---------------------------------------------------------------------------

// TestBuildEstate is the whole-fixture claim: given the estate's config and
// the identity package's real classification of it, a projection contains
// exactly the six client-named instances, and every other instance is
// absent with a reason that says which of the two P1 limits it hit.
//
// That the three parent-derived instances are all absent is not a
// shortcoming of this test. Under the v0 identity table every
// parent-derived formula bottoms out in a server-assigned parent, so in
// phase 1 no formula can be rendered at all; the ordering and rendering
// paths are exercised by the hand-written resolutions further down, which
// is the shape P2's discovery pass will produce.
func TestBuildEstate(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	resolutions := resolveOrFail(t, cfg)

	cloud := newFakeCloud()
	cloud.put("aws_s3_bucket", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
		"arn": "arn:aws:s3:::tofu-stateless-e2e-data",
	})
	cloud.put("aws_s3_bucket_policy", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
		"policy": `{"Version":"2012-10-17"}`,
	})
	cloud.put("aws_iam_role", "tofu-stateless-e2e-app", map[string]string{
		"id": "tofu-stateless-e2e-app", "name": "tofu-stateless-e2e-app",
		"arn": "arn:aws:iam::000000000000:role/tofu-stateless-e2e-app",
	})
	cloud.put("aws_iam_role_policy_attachment", "tofu-stateless-e2e-app/arn:aws:iam::aws:policy/ReadOnlyAccess", map[string]string{
		"id": "tofu-stateless-e2e-app-20260811", "role": "tofu-stateless-e2e-app",
		"policy_arn": "arn:aws:iam::aws:policy/ReadOnlyAccess",
	})
	cloud.put("aws_cloudwatch_log_group", "/stateless-e2e/app", map[string]string{
		"id": "/stateless-e2e/app", "name": "/stateless-e2e/app",
	})
	cloud.put("aws_cloudwatch_log_group", "/stateless-e2e/optional", map[string]string{
		"id": "/stateless-e2e/optional", "name": "/stateless-e2e/optional",
	})
	cloud.put("aws_ssm_parameter", "/tofu-receipts/stateless-e2e/demo-effect", map[string]string{
		"id": "/tofu-receipts/stateless-e2e/demo-effect", "name": "/tofu-receipts/stateless-e2e/demo-effect",
		"type": "String",
	})
	cloud.put("aws_ssm_parameter", "/tofu-receipts/stateless-e2e/demo-existence", map[string]string{
		"id": "/tofu-receipts/stateless-e2e/demo-existence", "name": "/tofu-receipts/stateless-e2e/demo-existence",
		"type": "String",
	})
	cloud.put("aws_dynamodb_table", "tofu-stateless-e2e-events", map[string]string{
		"id": "tofu-stateless-e2e-events", "name": "tofu-stateless-e2e-events",
		"arn": "arn:aws:dynamodb:us-east-1:000000000000:table/tofu-stateless-e2e-events",
	})
	// The cluster is keyed by its import ID (the name), while its id
	// attribute carries the ARN — the split the identity table records.
	cloud.put("aws_ecs_cluster", "tofu-stateless-e2e-cluster", map[string]string{
		"id":   "arn:aws:ecs:us-east-1:000000000000:cluster/tofu-stateless-e2e-cluster",
		"name": "tofu-stateless-e2e-cluster",
		"arn":  "arn:aws:ecs:us-east-1:000000000000:cluster/tofu-stateless-e2e-cluster",
	})
	// The four S3 bucket children (#19's second slice): each keyed by the
	// parent bucket's name, which is its whole import ID.
	cloud.put("aws_s3_bucket_versioning", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
	})
	cloud.put("aws_s3_bucket_public_access_block", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
	})
	cloud.put("aws_s3_bucket_server_side_encryption_configuration", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
	})
	cloud.put("aws_s3_bucket_lifecycle_configuration", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
	})
	// Same slice: the inline role policy is keyed by its role:name import
	// ID (which is also its id attribute), the alias by its full alias/...
	// name.
	cloud.put("aws_iam_role_policy", "tofu-stateless-e2e-app:tofu-stateless-e2e-app-inline", map[string]string{
		"id":   "tofu-stateless-e2e-app:tofu-stateless-e2e-app-inline",
		"role": "tofu-stateless-e2e-app", "name": "tofu-stateless-e2e-app-inline",
		"policy": `{"Version":"2012-10-17"}`,
	})
	cloud.put("aws_kms_alias", "alias/tofu-stateless-e2e-main", map[string]string{
		"id": "alias/tofu-stateless-e2e-main", "name": "alias/tofu-stateless-e2e-main",
		"target_key_id": "00000000-0000-0000-0000-000000000000",
	})
	cloud.put("aws_cloudwatch_metric_alarm", "tofu-stateless-e2e-cpu", map[string]string{
		"id": "tofu-stateless-e2e-cpu", "alarm_name": "tofu-stateless-e2e-cpu",
	})

	res, diags := Build(context.Background(), cfg, resolutions, cloud.providers(t))
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{
		`aws_cloudwatch_log_group.app`,
		`aws_cloudwatch_log_group.optional[0]`,
		`aws_cloudwatch_metric_alarm.cpu`,
		`aws_dynamodb_table.events`,
		`aws_ecs_cluster.app`,
		`aws_iam_role.app`,
		`aws_iam_role_policy.app`,
		`aws_iam_role_policy_attachment.app`,
		`aws_kms_alias.main`,
		`aws_s3_bucket.data`,
		`aws_s3_bucket_lifecycle_configuration.data`,
		`aws_s3_bucket_policy.data`,
		`aws_s3_bucket_public_access_block.data`,
		`aws_s3_bucket_server_side_encryption_configuration.data`,
		`aws_s3_bucket_versioning.data`,
		`aws_ssm_parameter.demo_effect`,
		`aws_ssm_parameter.demo_existence`,
	})

	assertOmitted(t, res, map[string]Reason{
		`aws_vpc.main`:                          ReasonNeedsDiscovery,
		`aws_subnet.this["a"]`:                  ReasonNeedsDiscovery,
		`aws_subnet.this["b"]`:                  ReasonNeedsDiscovery,
		`aws_security_group.main`:               ReasonNeedsDiscovery,
		`aws_route_table.main`:                  ReasonNeedsDiscovery,
		`aws_internet_gateway.main`:             ReasonNeedsDiscovery,
		`aws_eip.pool[0]`:                       ReasonNeedsDiscovery,
		`aws_eip.pool[1]`:                       ReasonNeedsDiscovery,
		`aws_eip.pool[2]`:                       ReasonNeedsDiscovery,
		`aws_kms_key.main`:                      ReasonNeedsDiscovery,
		`aws_route53_zone.main`:                 ReasonNeedsDiscovery,
		`aws_route.internet_gateway`:            ReasonParentUnavailable,
		`aws_route_table_association.this["a"]`: ReasonParentUnavailable,
		`aws_route_table_association.this["b"]`: ReasonParentUnavailable,
		`aws_route53_record.app`:                ReasonParentUnavailable,
	})

	// The reason for a parent-derived omission has to name the parent that
	// blocked it, otherwise an operator reading a plan that proposes
	// creating a route has no way to find out why.
	route := omissionFor(t, res, `aws_route.internet_gateway`)
	if !strings.Contains(route.Detail, "aws_route_table.main") {
		t.Errorf("the route's omission does not name the route table that blocked it:\n%s", route.Detail)
	}
	if !strings.Contains(route.Detail, "route table ID") {
		t.Errorf("the route's omission does not carry the parent's own reason through:\n%s", route.Detail)
	}
}

// TestBuildEstateObjects checks what actually lands in the state, as
// opposed to which addresses are present: the provider's read result, at
// the right address, under the right provider, with dependencies recorded.
func TestBuildEstateObjects(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	resolutions := resolveOrFail(t, cfg)

	cloud := newFakeCloud()
	cloud.put("aws_s3_bucket", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
		"arn": "arn:aws:s3:::tofu-stateless-e2e-data",
	})
	cloud.put("aws_s3_bucket_policy", "tofu-stateless-e2e-data", map[string]string{
		"id": "tofu-stateless-e2e-data", "bucket": "tofu-stateless-e2e-data",
		"policy": `{"Version":"2012-10-17"}`,
	})
	cloud.put("aws_iam_role", "tofu-stateless-e2e-app", map[string]string{
		"id": "tofu-stateless-e2e-app", "name": "tofu-stateless-e2e-app",
		"arn": "arn:aws:iam::000000000000:role/tofu-stateless-e2e-app",
	})

	res, diags := Build(context.Background(), cfg, resolutions, cloud.providers(t))
	assertNoErrors(t, diags)

	is := res.State.ResourceInstance(mustAddr(t, `aws_s3_bucket.data`))
	if is == nil || is.Current == nil {
		t.Fatal("aws_s3_bucket.data is not in the projection")
	}
	if got, want := string(is.Current.AttrsJSON), `"arn:aws:s3:::tofu-stateless-e2e-data"`; !strings.Contains(got, want) {
		t.Errorf("the bucket's attributes do not carry the value the provider read:\n%s", got)
	}
	if is.Current.Status != 'R' {
		t.Errorf("the bucket object's status is %q, want ObjectReady", is.Current.Status)
	}

	rs := res.State.Resource(mustAddr(t, `aws_s3_bucket.data`).ContainingResource())
	if got, want := rs.ProviderConfig.String(), awsProvider.String(); got != want {
		t.Errorf("the bucket is filed under provider %s, want %s", got, want)
	}

	// The bucket policy's body references the role's ARN and the bucket's
	// ARN, which is what a plan needs in order to destroy it in the right
	// order once its configuration is gone.
	policy := res.State.ResourceInstance(mustAddr(t, `aws_s3_bucket_policy.data`))
	if policy == nil || policy.Current == nil {
		t.Fatal("aws_s3_bucket_policy.data is not in the projection")
	}
	var deps []string
	for _, d := range policy.Current.Dependencies {
		deps = append(deps, d.String())
	}
	sort.Strings(deps)
	if got, want := strings.Join(deps, ","), "aws_iam_role.app,aws_s3_bucket.data"; got != want {
		t.Errorf("the bucket policy records dependencies %q, want %q", got, want)
	}
}

// TestBuildEstateAllAbsent is the empty-cloud case: nothing exists, so the
// projection is empty and nothing is an error. This is the state a plan
// against a fresh account must see, since every instance being absent is
// what makes the plan propose creating all of them.
func TestBuildEstateAllAbsent(t *testing.T) {
	cfg := loadConfig(t, estateDir(t))
	resolutions := resolveOrFail(t, cfg)

	res, diags := Build(context.Background(), cfg, resolutions, newFakeCloud().providers(t))
	assertNoErrors(t, diags)

	if len(res.Materialized) != 0 {
		t.Errorf("projection is not empty against an empty cloud: %s", res)
	}
	if got, want := len(res.OmittedBecause(ReasonAbsent)), 17; got != want {
		t.Errorf("%d instances recorded as absent, want %d:\n%s", got, want, res)
	}
	if res.State.HasManagedResourceInstanceObjects() {
		t.Error("the returned state holds resource objects when nothing was materialized")
	}
}

// ---------------------------------------------------------------------------
// Ordering and formula rendering, over hand-written resolutions.
// ---------------------------------------------------------------------------

// TestBuildRendersFormulaFromParentState is the core parent-derived claim.
// The route table is materialized first, and the route's import ID is
// rendered from the route table's *live* id as read back by the provider
// (rtb-live), not from the import identity the route table was looked up
// with (rtb-imported). Those two strings differ on purpose: a projection
// that rendered formulas from resolution data rather than from what the
// cloud actually returned would pass a weaker test and fail here.
func TestBuildRendersFormulaFromParentState(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	cloud := newFakeCloud()
	cloud.put("aws_route_table", "rtb-imported", map[string]string{
		"id": "rtb-live", "vpc_id": "vpc-fixed",
	})
	cloud.put("aws_route", "rtb-live_0.0.0.0/0", map[string]string{
		"id": "r-rtb-live1080289494", "route_table_id": "rtb-live",
		"destination_cidr_block": "0.0.0.0/0",
	})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		// Deliberately child-first: ordering is the builder's job.
		derivedOn(route, rtb, "id", "_0.0.0.0/0"),
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, cloud.providers(t))
	assertNoErrors(t, diags)

	assertMaterialized(t, res, []string{
		`aws_route.internet_gateway`,
		`aws_route_table.main`,
	})

	wantImports := []string{
		"aws_route_table/rtb-imported",
		"aws_route/rtb-live_0.0.0.0/0",
	}
	if got := cloud.imports; !slices.Equal(got, wantImports) {
		t.Errorf("provider was asked to import\n %v\nwant\n %v", got, wantImports)
	}
}

// TestBuildOrdersChainTopologically hands the builder a three-deep
// derivation chain in exactly the wrong order, to check that ordering is a
// real topological sort and not a single pass that happens to work when
// the input is already sorted.
func TestBuildOrdersChainTopologically(t *testing.T) {
	cfg := loadConfig(t, "testdata/chain")

	subnet := mustAddr(t, `aws_subnet.a`)
	rtb := mustAddr(t, `aws_route_table.main`)
	assoc := mustAddr(t, `aws_route_table_association.a`)

	cloud := newFakeCloud()
	cloud.put("aws_subnet", "subnet-imported", map[string]string{"id": "subnet-live"})
	cloud.put("aws_route_table", "subnet-live-table", map[string]string{"id": "rtb-live"})
	cloud.put("aws_route_table_association", "subnet-live/rtb-live", map[string]string{
		"id": "rtbassoc-live", "subnet_id": "subnet-live", "route_table_id": "rtb-live",
	})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{
			Addr:  assoc,
			Class: identity.ClassParentDerived,
			Formula: &identity.Formula{
				Parts: []identity.Part{
					{Parent: &identity.ParentRef{Instance: subnet, Attr: "id"}},
					{Literal: "/"},
					{Parent: &identity.ParentRef{Instance: rtb, Attr: "id"}},
				},
				Parents: []addrs.AbsResourceInstance{rtb, subnet},
			},
		},
		derivedOn(rtb, subnet, "id", "-table"),
		{Addr: subnet, Class: identity.ClassConcrete, ImportID: "subnet-imported"},
	}, cloud.providers(t))
	assertNoErrors(t, diags)

	wantImports := []string{
		"aws_subnet/subnet-imported",
		"aws_route_table/subnet-live-table",
		"aws_route_table_association/subnet-live/rtb-live",
	}
	if got := cloud.imports; !slices.Equal(got, wantImports) {
		t.Errorf("provider was asked to import\n %v\nwant\n %v", got, wantImports)
	}
	if len(res.Materialized) != 3 {
		t.Errorf("materialized %d instances, want 3:\n%s", len(res.Materialized), res)
	}
}

// TestBuildParentAbsentBlocksChild: a parent that simply does not exist
// live is not an error, and its child is omitted rather than being
// imported with a hole where the parent's ID should be.
func TestBuildParentAbsentBlocksChild(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	cloud := newFakeCloud() // empty: neither object exists

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
		derivedOn(route, rtb, "id", "_0.0.0.0/0"),
	}, cloud.providers(t))
	assertNoErrors(t, diags)

	if len(res.Materialized) != 0 {
		t.Errorf("something was materialized against an empty cloud:\n%s", res)
	}
	assertOmitted(t, res, map[string]Reason{
		`aws_route_table.main`:       ReasonAbsent,
		`aws_route.internet_gateway`: ReasonParentUnavailable,
	})

	// The route never got as far as an import call: there was no identity
	// to import with.
	if len(cloud.imports) != 1 || cloud.imports[0] != "aws_route_table/rtb-imported" {
		t.Errorf("provider calls were %v, want only the route table lookup", cloud.imports)
	}

	// The chain has to be legible: the route is missing because the route
	// table is missing, and the route table is missing because it does not
	// exist.
	route0 := omissionFor(t, res, `aws_route.internet_gateway`)
	if !strings.Contains(route0.Detail, "no aws_route_table exists with identity") {
		t.Errorf("the route's omission does not carry the route table's own reason:\n%s", route0.Detail)
	}
}

// TestBuildNeedsDiscoveryParentBlocksChild is the same shape with the
// parent unresolvable rather than absent, which is the estate's real
// situation until P2.
func TestBuildNeedsDiscoveryParentBlocksChild(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	cloud := newFakeCloud()
	cloud.put("aws_route_table", "rtb-imported", map[string]string{"id": "rtb-live"})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassNeedsDiscovery, Reason: "EC2 assigns the route table ID at create time."},
		derivedOn(route, rtb, "id", "_0.0.0.0/0"),
	}, cloud.providers(t))
	assertNoErrors(t, diags)

	assertOmitted(t, res, map[string]Reason{
		`aws_route_table.main`:       ReasonNeedsDiscovery,
		`aws_route.internet_gateway`: ReasonParentUnavailable,
	})
	if len(cloud.imports) != 0 {
		t.Errorf("the provider was called at all: %v. A needs-discovery instance has no identity to call with.", cloud.imports)
	}
}

// TestBuildCyclicFormulas: two parent-derived instances that name each
// other cannot be ordered. That is a bug upstream in identity resolution
// rather than a fact about the cloud, so it is an error, not a quiet
// omission.
func TestBuildCyclicFormulas(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		derivedOn(rtb, route, "id", "-x"),
		derivedOn(route, rtb, "id", "-y"),
	}, newFakeCloud().providers(t))

	if !diags.HasErrors() {
		t.Fatal("a cycle among parent-derived identities produced no error")
	}
	if !hasDiag(diags, "Cyclic parent-derived identities", "cycle") {
		t.Errorf("wrong diagnostics for a cycle:\n%s", renderDiags(diags))
	}
	assertOmitted(t, res, map[string]Reason{
		`aws_route_table.main`:       ReasonCycle,
		`aws_route.internet_gateway`: ReasonCycle,
	})
}

// TestBuildParentMissingIdentityAttr: the formula reads an attribute the
// provider's object does not carry. Rendering half an identity would
// silently bind the wrong object, so this is an error.
func TestBuildParentMissingIdentityAttr(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	rtb := mustAddr(t, `aws_route_table.main`)
	route := mustAddr(t, `aws_route.internet_gateway`)

	cloud := newFakeCloud()
	// id is present in the schema but null in the object the provider read.
	cloud.put("aws_route_table", "rtb-imported", map[string]string{"vpc_id": "vpc-fixed"})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
		derivedOn(route, rtb, "id", "_0.0.0.0/0"),
	}, cloud.providers(t))

	if !diags.HasErrors() {
		t.Fatal("a parent with no usable identity attribute produced no error")
	}
	if !hasDiag(diags, "Cannot read a parent's identity from the projection", `the value of "id" is null`) {
		t.Errorf("wrong diagnostics:\n%s", renderDiags(diags))
	}
	assertOmitted(t, res, map[string]Reason{
		`aws_route.internet_gateway`: ReasonFailed,
	})
	if !res.Has(rtb) {
		t.Error("the parent itself should still be in the projection; only the child failed")
	}
}

// ---------------------------------------------------------------------------
// Absence versus failure.
// ---------------------------------------------------------------------------

// TestBuildAbsentByEmptyImport covers the first of the two ways a provider
// says "no such object": ImportResourceState returns no imported resources
// at all.
func TestBuildAbsentByEmptyImport(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")
	rtb := mustAddr(t, `aws_route_table.main`)

	cloud := newFakeCloud()
	cloud.emptyImports["aws_route_table/rtb-imported"] = true

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, cloud.providers(t))

	assertNoErrors(t, diags)
	assertOmitted(t, res, map[string]Reason{`aws_route_table.main`: ReasonAbsent})
	if cloud.reads != 0 {
		t.Errorf("the provider was asked to read %d times after an empty import; there was nothing to read", cloud.reads)
	}
}

// TestBuildAbsentByNullRead covers the second and more common way: the
// import succeeds with a stub, and the refresh comes back null. This is
// what the AWS provider does for most types.
func TestBuildAbsentByNullRead(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")
	rtb := mustAddr(t, `aws_route_table.main`)

	cloud := newFakeCloud() // import returns a stub; nothing is in the cloud

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, cloud.providers(t))

	assertNoErrors(t, diags)
	assertOmitted(t, res, map[string]Reason{`aws_route_table.main`: ReasonAbsent})
	if cloud.reads != 1 {
		t.Errorf("the provider was read %d times, want 1", cloud.reads)
	}
	o := omissionFor(t, res, `aws_route_table.main`)
	if !strings.Contains(o.Detail, "rtb-imported") {
		t.Errorf("the absence reason does not name the identity that was looked up:\n%s", o.Detail)
	}
}

// TestBuildImportError: an import that errors is a provider that could not
// answer, which is a different thing from an object that is not there, and
// has to reach the caller as an error.
func TestBuildImportError(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")
	rtb := mustAddr(t, `aws_route_table.main`)

	cloud := newFakeCloud()
	cloud.importErrors["aws_route_table/rtb-imported"] = "AccessDenied: not authorized to perform ec2:DescribeRouteTables"

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, cloud.providers(t))

	if !diags.HasErrors() {
		t.Fatal("an erroring import produced no error diagnostics")
	}
	if !hasDiag(diags, "Cannot import for projection", "rtb-imported") {
		t.Errorf("the import failure is not reported as such:\n%s", renderDiags(diags))
	}
	if !strings.Contains(renderDiags(diags), "AccessDenied") {
		t.Errorf("the provider's own message was dropped:\n%s", renderDiags(diags))
	}
	assertOmitted(t, res, map[string]Reason{`aws_route_table.main`: ReasonFailed})
}

// TestBuildReadError: the same, one call later.
func TestBuildReadError(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")
	rtb := mustAddr(t, `aws_route_table.main`)

	cloud := newFakeCloud()
	cloud.readErrors["aws_route_table/rtb-imported"] = "RequestLimitExceeded"

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, cloud.providers(t))

	if !diags.HasErrors() {
		t.Fatal("an erroring read produced no error diagnostics")
	}
	if !hasDiag(diags, "Cannot read for projection", "rtb-imported") {
		t.Errorf("the read failure is not reported as such:\n%s", renderDiags(diags))
	}
	assertOmitted(t, res, map[string]Reason{`aws_route_table.main`: ReasonFailed})
}

// TestBuildOneFailureDoesNotStopTheRest: a projection reports everything it
// can, so that an operator sees one error rather than a series of them one
// run at a time.
func TestBuildOneFailureDoesNotStopTheRest(t *testing.T) {
	cfg := loadConfig(t, "testdata/chain")

	subnet := mustAddr(t, `aws_subnet.a`)
	rtb := mustAddr(t, `aws_route_table.main`)

	cloud := newFakeCloud()
	cloud.importErrors["aws_subnet/subnet-imported"] = "boom"
	cloud.put("aws_route_table", "rtb-imported", map[string]string{"id": "rtb-live"})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: subnet, Class: identity.ClassConcrete, ImportID: "subnet-imported"},
		{Addr: rtb, Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, cloud.providers(t))

	if !diags.HasErrors() {
		t.Fatal("the failing instance produced no error")
	}
	if !res.Has(rtb) {
		t.Errorf("the healthy instance was dropped because a different one failed:\n%s", res)
	}
}

// TestBuildProviderUnavailable: a provider that will not start is an error
// against every instance that needed it, but it is only started once.
func TestBuildProviderUnavailable(t *testing.T) {
	cfg := loadConfig(t, "testdata/chain")

	calls := 0
	provs := ProviderFunc(func(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
		calls++
		return nil, fmt.Errorf("plugin exited before we could connect")
	})

	res, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_subnet.a`), Class: identity.ClassConcrete, ImportID: "subnet-imported"},
		{Addr: mustAddr(t, `aws_route_table.main`), Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, provs)

	if !diags.HasErrors() {
		t.Fatal("an unreachable provider produced no error")
	}
	if calls != 1 {
		t.Errorf("the provider was launched %d times, want 1: a failure to start must be sticky", calls)
	}
	if len(res.OmittedBecause(ReasonFailed)) != 2 {
		t.Errorf("want both instances recorded as failed, got:\n%s", res)
	}
}

// TestSingleProviderRefusesUnknownConfig pins the honest-limit behaviour of
// the helper the tests and P1.4's first cut use.
func TestSingleProviderRefusesUnknownConfig(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")

	other := addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: addrs.NewDefaultProvider("null")}
	provs := SingleProvider(other, newFakeCloud().provider(t))

	_, diags := BuildFrom(context.Background(), cfg, []identity.Resolution{
		{Addr: mustAddr(t, `aws_route_table.main`), Class: identity.ClassConcrete, ImportID: "rtb-imported"},
	}, provs)

	if !hasDiag(diags, "Provider unavailable", "no provider configured for") {
		t.Errorf("wrong diagnostics for a missing provider configuration:\n%s", renderDiags(diags))
	}
}

// TestBuildRejectsBadInput covers the guards. None of these can happen from
// a correctly wired caller, which is exactly why they should say so rather
// than panic.
func TestBuildRejectsBadInput(t *testing.T) {
	cfg := loadConfig(t, "testdata/derived")
	cloud := newFakeCloud()

	if _, diags := Build(context.Background(), nil, nil, cloud.providers(t)); !hasDiag(diags, "No configuration to project", "") {
		t.Errorf("nil config:\n%s", renderDiags(diags))
	}
	if _, diags := Build(context.Background(), cfg, nil, cloud.providers(t)); !hasDiag(diags, "No identity resolutions to project", "") {
		t.Errorf("nil resolutions:\n%s", renderDiags(diags))
	}
	if _, diags := BuildFrom(context.Background(), cfg, nil, nil); !hasDiag(diags, "No provider access", "") {
		t.Errorf("nil providers:\n%s", renderDiags(diags))
	}
}

// ---------------------------------------------------------------------------
// The fake cloud.
// ---------------------------------------------------------------------------

// fakeCloud is a tiny stand-in for the live system, driven through
// tofu.MockProvider. It answers the two calls a projection makes and
// nothing else, which is the point: if this package ever starts needing a
// third provider call, this fake stops compiling into a passing test.
type fakeCloud struct {
	// objects maps "type/importID" to the attributes ReadResource returns.
	objects map[string]map[string]string
	// tags maps "type/importID" to the tags ReadResource returns on the
	// object, which is where ownership markers live.
	tags map[string]map[string]string
	// emptyImports maps "type/importID" to "return no imported resources".
	emptyImports map[string]bool
	importErrors map[string]string
	readErrors   map[string]string

	imports []string
	reads   int
}

func newFakeCloud() *fakeCloud {
	return &fakeCloud{
		objects:      make(map[string]map[string]string),
		tags:         make(map[string]map[string]string),
		emptyImports: make(map[string]bool),
		importErrors: make(map[string]string),
		readErrors:   make(map[string]string),
	}
}

func (c *fakeCloud) put(typeName, importID string, attrs map[string]string) {
	c.objects[typeName+"/"+importID] = attrs
}

// putTagged is put for a live object that carries tags - ownership markers
// among them, which is what the projection reads before it admits anything
// into prior state. A nil map is a live resource with no tags at all, which
// is what an unowned one looks like.
func (c *fakeCloud) putTagged(typeName, importID string, attrs, tags map[string]string) {
	c.put(typeName, importID, attrs)
	if tags != nil {
		c.tags[typeName+"/"+importID] = tags
	}
}

func (c *fakeCloud) providers(t *testing.T) Providers {
	return SingleProvider(awsProvider, c.provider(t))
}

func (c *fakeCloud) provider(t *testing.T) providers.Interface {
	t.Helper()

	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider:      providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: fakeSchemas(),
		},
	}
	// Build never configures a provider: that is the caller's job, and the
	// mock refuses to serve anything until it has happened.
	p.ConfigureProviderCalled = true

	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var resp providers.ImportResourceStateResponse
		key := r.TypeName + "/" + r.Target.ID
		c.imports = append(c.imports, key)

		if msg, ok := c.importErrors[key]; ok {
			resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("%s", msg))
			return resp
		}
		if c.emptyImports[key] {
			return resp
		}

		schema := fakeSchemas()[r.TypeName]
		stub := objectFor(schema, map[string]string{"id": r.Target.ID})
		resp.ImportedResources = []providers.ImportedResource{{
			TypeName: r.TypeName,
			State:    stub,
		}}
		return resp
	}

	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		var resp providers.ReadResourceResponse
		c.reads++

		schema := fakeSchemas()[r.TypeName]
		id := r.PriorState.GetAttr("id")
		key := r.TypeName + "/" + id.AsString()

		if msg, ok := c.readErrors[key]; ok {
			resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("%s", msg))
			return resp
		}
		attrs, ok := c.objects[key]
		if !ok {
			resp.NewState = cty.NullVal(schema.Block.ImpliedType())
			return resp
		}
		resp.NewState = objectWithTags(schema, attrs, c.tags[key])
		return resp
	}

	return p
}

// fakeAttrs is the attribute set of each resource type the fixtures use.
// It is a caricature of the AWS provider's schema: enough attributes for
// the fixtures' bodies to decode (which is what dependency extraction
// needs) and no more.
var fakeAttrs = map[string][]string{
	"aws_s3_bucket":                                      {"id", "bucket", "arn"},
	"aws_s3_bucket_policy":                               {"id", "bucket", "policy"},
	"aws_s3_bucket_versioning":                           {"id", "bucket"},
	"aws_s3_bucket_public_access_block":                  {"id", "bucket", "block_public_acls", "block_public_policy", "ignore_public_acls", "restrict_public_buckets"},
	"aws_s3_bucket_server_side_encryption_configuration": {"id", "bucket"},
	"aws_s3_bucket_lifecycle_configuration":              {"id", "bucket"},
	"aws_iam_role":                                       {"id", "name", "arn", "assume_role_policy"},
	"aws_iam_role_policy_attachment":                     {"id", "role", "policy_arn"},
	"aws_cloudwatch_log_group":                           {"id", "name", "arn"},
	"aws_vpc":                                            {"id", "cidr_block"},
	"aws_subnet":                                         {"id", "vpc_id", "cidr_block", "availability_zone"},
	"aws_security_group":                                 {"id", "name", "description", "vpc_id"},
	"aws_route_table":                                    {"id", "vpc_id"},
	"aws_internet_gateway":                               {"id", "vpc_id"},
	"aws_route":                                          {"id", "route_table_id", "destination_cidr_block", "gateway_id"},
	"aws_route_table_association":                        {"id", "subnet_id", "route_table_id"},
	"aws_eip":                                            {"id", "domain", "allocation_id"},
	"aws_ssm_parameter":                                  {"id", "name", "value", "type"},
	"aws_dynamodb_table":                                 {"id", "name", "arn", "billing_mode", "hash_key"},
	"aws_ecs_cluster":                                    {"id", "name", "arn"},
	"aws_kms_key":                                        {"id", "key_id", "arn", "description"},
	// The zone's identity attribute is zone_id rather than id, and both
	// carry the hosted zone ID; the caricature keeps both for that reason.
	"aws_route53_zone":    {"id", "zone_id", "arn", "name"},
	"aws_iam_role_policy": {"id", "role", "name", "policy"},
	"aws_kms_alias":       {"id", "name", "target_key_id"},
	"aws_route53_record":  {"id", "zone_id", "name", "type", "ttl", "records"},
	"aws_cloudwatch_metric_alarm": {"id", "alarm_name", "comparison_operator", "evaluation_periods",
		"metric_name", "namespace", "period", "statistic", "threshold"},
}

// fakeUntaggable is the caricature's version of a fact about the real
// provider: some types in the v0 subset have no tags argument at all, so they
// can carry no ownership marker and the projection's ownership check has
// nothing to read on them. Modelled rather than smoothed over, because
// "admitted because it cannot be marked" is a decision the tests have to be
// able to see.
var fakeUntaggable = map[string]bool{
	"aws_s3_bucket_policy":           true,
	"aws_iam_role_policy_attachment": true,
	"aws_route":                      true,
	"aws_route_table_association":    true,
}

func fakeSchemas() map[string]providers.Schema {
	out := make(map[string]providers.Schema, len(fakeAttrs))
	for typeName, names := range fakeAttrs {
		attrs := map[string]*configschema.Attribute{}
		if !fakeUntaggable[typeName] {
			attrs["tags"] = &configschema.Attribute{Type: cty.Map(cty.String), Optional: true}
		}
		for _, n := range names {
			attrs[n] = &configschema.Attribute{Type: cty.String, Optional: true, Computed: true}
		}
		out[typeName] = providers.Schema{
			Version: 0,
			Block:   &configschema.Block{Attributes: attrs},
		}
	}
	return out
}

// objectFor builds a value of a fake schema's implied type, with the named
// attributes set and everything else null.
func objectFor(schema providers.Schema, attrs map[string]string) cty.Value {
	return objectWithTags(schema, attrs, nil)
}

// objectWithTags is objectFor with the tags map the live object carries.
func objectWithTags(schema providers.Schema, attrs, tags map[string]string) cty.Value {
	vals := make(map[string]cty.Value, len(schema.Block.Attributes))
	for name, at := range schema.Block.Attributes {
		if v, ok := attrs[name]; ok && at.Type == cty.String {
			vals[name] = cty.StringVal(v)
			continue
		}
		vals[name] = cty.NullVal(at.Type)
	}
	if _, ok := schema.Block.Attributes["tags"]; ok && len(tags) > 0 {
		tagVals := make(map[string]cty.Value, len(tags))
		for k, v := range tags {
			tagVals[k] = cty.StringVal(v)
		}
		vals["tags"] = cty.MapVal(tagVals)
	}
	return cty.ObjectVal(vals)
}

// ---------------------------------------------------------------------------
// Test helpers.
// ---------------------------------------------------------------------------

// derivedOn writes the commonest parent-derived resolution by hand: one
// parent attribute followed by a literal suffix.
func derivedOn(addr, parent addrs.AbsResourceInstance, attr, suffix string) identity.Resolution {
	return identity.Resolution{
		Addr:  addr,
		Class: identity.ClassParentDerived,
		Formula: &identity.Formula{
			Parts: []identity.Part{
				{Parent: &identity.ParentRef{Instance: parent, Attr: attr}},
				{Literal: suffix},
			},
			Parents: []addrs.AbsResourceInstance{parent},
		},
	}
}

func loadConfig(t *testing.T, dir string) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir,
		"default",
	)

	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(context.Background(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			t.Fatalf("test fixture %s unexpectedly calls module %q", dir, req.Name)
			return nil, nil, nil
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}

func resolveOrFail(t *testing.T, cfg *configs.Config) *identity.Result {
	t.Helper()
	res, diags := identity.Resolve(context.Background(), cfg)
	if diags.HasErrors() {
		t.Fatalf("identity resolution failed on a fixture it is supposed to handle:\n%s", renderDiags(diags))
	}
	return res
}

func assertMaterialized(t *testing.T, res *Result, want []string) {
	t.Helper()

	var got []string
	for _, addr := range res.Materialized {
		got = append(got, addr.String())
		if !res.Has(addr) {
			t.Errorf("%s is listed as materialized but is not in the state", addr)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("materialized\n %v\nwant\n %v\nfull result:\n%s", got, want, res)
	}
}

func assertOmitted(t *testing.T, res *Result, want map[string]Reason) {
	t.Helper()

	got := make(map[string]Reason, len(res.Omitted))
	for _, o := range res.Omitted {
		got[o.Addr.String()] = o.Reason
		if o.Detail == "" {
			t.Errorf("%s is omitted with no explanation", o.Addr)
		}
	}
	for addr, reason := range want {
		switch g, ok := got[addr]; {
		case !ok:
			t.Errorf("%s is not recorded as omitted; want %s", addr, reason)
		case g != reason:
			t.Errorf("%s is omitted as %s, want %s", addr, g, reason)
		}
	}
	if len(want) > 0 && len(got) != len(want) {
		// Only checked when the test enumerates the full expectation.
		var extra []string
		for addr := range got {
			if _, ok := want[addr]; !ok {
				extra = append(extra, addr)
			}
		}
		sort.Strings(extra)
		if len(extra) > 0 {
			t.Errorf("unexpected omissions: %v\nfull result:\n%s", extra, res)
		}
	}
}

func omissionFor(t *testing.T, res *Result, addr string) Omission {
	t.Helper()
	o, ok := res.OmissionFor(mustAddr(t, addr))
	if !ok {
		t.Fatalf("%s has no recorded omission:\n%s", addr, res)
	}
	return o
}

func assertNoErrors(t *testing.T, diags tfdiags.Diagnostics) {
	t.Helper()
	if diags.HasErrors() {
		t.Fatalf("unexpected diagnostics:\n%s", renderDiags(diags))
	}
}

func hasDiag(diags tfdiags.Diagnostics, summary, detailFragment string) bool {
	for _, d := range diags {
		desc := d.Description()
		if desc.Summary == summary && strings.Contains(desc.Detail, detailFragment) {
			return true
		}
	}
	return false
}

func renderDiags(diags tfdiags.Diagnostics) string {
	var buf strings.Builder
	for _, d := range diags {
		desc := d.Description()
		fmt.Fprintf(&buf, "- [%s] %s: %s\n", d.Severity(), desc.Summary, desc.Detail)
	}
	return buf.String()
}

func mustAddr(t *testing.T, s string) addrs.AbsResourceInstance {
	t.Helper()
	addr, diags := addrs.ParseAbsResourceInstanceStr(s)
	if diags.HasErrors() {
		t.Fatalf("bad test address %q: %s", s, diags.Err())
	}
	return addr
}
