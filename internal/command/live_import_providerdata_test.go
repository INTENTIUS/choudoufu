// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/command/workdir"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/dataread"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/terminal"
	"github.com/intentius/choudoufu/internal/tofu"
	"github.com/zclconf/go-cty/cty"
)

// TestLiveImportConfiguresAProviderFromADataSource is GitHub issue #1543's
// red proof: a provider configuration whose own arguments read a data source
// is configurable during a migration, the same way it already is during a
// plan.
//
// The fixture is corpus-eks-basic's shape reduced to one registered provider
// factory: the default aws configuration is literal, a data source is read
// through it, and a SECOND provider configuration - aws.derived here,
// provider "kubernetes" there - takes an argument from that data source's
// value. The state names the aliased configuration as the resource's own, so
// ratifying that one instance is what forces the aliased block to be
// evaluated.
//
// Before the fix, ratifyOne's ConfiguredProvider call failed with
// "Dynamic value in static context" and the instance was reported MISSING,
// so -approve wrote no marker at all - which is exactly why
// kube-system/aws-auth carried no tofu-estate label after a migrate.
func TestLiveImportConfiguresAProviderFromADataSource(t *testing.T) {
	cloud := importNewCloud()
	cloud.put("aws_s3_bucket", "tofu-import-unit-data",
		map[string]string{"id": "tofu-import-unit-data", "bucket": "tofu-import-unit-data"},
		map[string]string{})

	prov := providerDataImportProvider(cloud)
	c, done := newLiveImportCommandIn(t, "live-import-providerdata", prov)
	code := c.Run([]string{"-no-color", "-state=import.tfstate", "-estate=stateless-unit", "-approve"})
	output := done(t)
	stdout, stderr := output.Stdout(), output.Stderr()

	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "MISSING") {
		t.Errorf("the instance was reported MISSING; the aliased provider was not configured:\n%s", stdout)
	}
	if strings.Contains(stdout, "Dynamic value in static context") {
		t.Errorf("the provider-configuration data read did not happen:\n%s", stdout)
	}
	if !strings.Contains(stdout, "STAMPED") {
		t.Errorf("the instance was not stamped:\n%s", stdout)
	}

	tags := cloud.tagsOf("aws_s3_bucket", "tofu-import-unit-data")
	if tags["tofu-estate"] != "stateless-unit" {
		t.Errorf("tofu-estate = %q, want stateless-unit\nstdout:\n%s", tags["tofu-estate"], stdout)
	}
	if tags["tofu-address"] != "aws_s3_bucket.data" {
		t.Errorf("tofu-address = %q, want aws_s3_bucket.data", tags["tofu-address"])
	}

	// The value the aliased block was configured with came from the data
	// source's own answer, not from a default or the unaliased block: the
	// read is load-bearing, not merely survived.
	if !prov.configuredWith("us-west-2") {
		t.Errorf("no provider configuration took its region from the data source; configured with %v", prov.regions)
	}
}

// newLiveImportCommandIn is [newLiveImportCommand] with the fixture
// directory and the provider chosen by the caller, for a test whose
// configuration is not testdata/live-import-basic's single literal provider
// block.
// extra is an optional provider/factory pair - one more registered provider
// for a fixture that needs two.
func newLiveImportCommandIn(t *testing.T, fixture string, prov providers.Interface, extra ...any) (*LiveImportCommand, func(*testing.T) *terminal.TestOutput) {
	t.Helper()

	td := t.TempDir()
	testCopyDir(t, testFixturePath(fixture), td)
	t.Chdir(td)

	factories := map[addrs.Provider]providers.Factory{
		addrs.NewDefaultProvider("aws"): providers.FactoryFixed(prov),
	}
	for i := 0; i+1 < len(extra); i += 2 {
		factories[extra[i].(addrs.Provider)] = extra[i+1].(providers.Factory)
	}

	view, done := testView(t)
	c := &LiveImportCommand{
		Meta: Meta{
			WorkingDir: workdir.NewDir("."),
			View:       view,
			testingOverrides: &testingOverrides{
				Providers: factories,
			},
		},
	}
	return c, done
}

// providerDataImportCloud is [importCloud]'s provider with a data source
// added - the one the aliased provider block reads - and a record of the
// region every ConfigureProvider call carried.
type providerDataImportCloud struct {
	providers.Interface
	regions []string

	// onlyForBucket, when set, is the one bucket argument the data source
	// answers for: a run that reached it with any other value gets nothing
	// back, so a value invented somewhere other than the state file cannot
	// pass.
	onlyForBucket string
}

func (p *providerDataImportCloud) configuredWith(region string) bool {
	for _, r := range p.regions {
		if r == region {
			return true
		}
	}
	return false
}

func providerDataImportProvider(c *importCloud) *providerDataImportCloud {
	out := &providerDataImportCloud{}

	inner := c.provider().(*tofu.MockProvider)
	inner.GetProviderSchemaResponse.DataSources = map[string]providers.Schema{
		"aws_s3_bucket": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
			"id":     {Type: cty.String, Computed: true},
			"bucket": {Type: cty.String, Required: true},
			"region": {Type: cty.String, Computed: true},
		}}},
	}
	inner.ReadDataSourceFn = func(r providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
		bucket := r.Config.GetAttr("bucket")
		region := cty.StringVal("us-west-2")
		if out.onlyForBucket != "" && (bucket.IsNull() || !bucket.IsKnown() || bucket.AsString() != out.onlyForBucket) {
			region = cty.NullVal(cty.String)
		}
		return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
			"id":     bucket,
			"bucket": bucket,
			"region": region,
		})}
	}
	inner.ConfigureProviderFn = func(r providers.ConfigureProviderRequest) providers.ConfigureProviderResponse {
		region := r.Config.GetAttr("region")
		if !region.IsNull() && region.IsKnown() {
			out.regions = append(out.regions, region.AsString())
		}
		return providers.ConfigureProviderResponse{}
	}
	inner.ConfigureProviderCalled = false

	out.Interface = inner
	return out
}

// TestLiveImportProviderDataGateIsOfflineAndExact pins the one thing that
// makes GitHub issue #1543's phase free for every estate that migrated
// before it existed: [liveImportProviderDataReads] returns on
// [dataread.Analysis.Empty] before it reads a schema, starts a plugin or
// resolves anything, and Empty answers "do this configuration's provider
// blocks reach a declared data source" and nothing else.
//
// Asserted against both fixtures at once, because a gate that answered
// "empty" for both would also make this file's other test pass for the
// wrong reason - it would simply never have run - and a gate that answered
// "not empty" for both would put the whole phase in front of every
// migration.
//
// It is the analysis expression itself, not a paraphrase of it: changing
// the call in live_import.go without changing it here is what this test is
// for.
func TestLiveImportProviderDataGateIsOfflineAndExact(t *testing.T) {
	for _, tc := range []struct {
		fixture string
		empty   bool
	}{
		{"live-import-basic", true},
		{"live-import-providerdata", false},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			cfg := statelessTestLoadConfig(t, testFixturePath(tc.fixture))
			// No schemas, no providers, no scope - the same bare options
			// liveImportProviderDataReads passes.
			got := dataread.AnalyzeProviderConfigs(t.Context(), cfg, dataread.Options{}).Empty()
			if got != tc.empty {
				t.Errorf("AnalyzeProviderConfigs(...).Empty() = %v, want %v", got, tc.empty)
			}
		})
	}
}

// TestLiveImportReadsProviderDataThroughTheStateItIsMigrating is the second
// half of GitHub issue #1543, and it is the half corpus-eks-basic actually
// needed: running the phase is not enough, because the phase reads a
// record-backed instance out of the estate's record store and a migration is
// what SEEDS that store. At Ratify time it is empty, so the one value the
// chain turns on cannot be materialized by reading anything.
//
// The fixture is the measured chain with the names changed:
// data.aws_s3_bucket.config needs aws_s3_bucket.data's server-assigned id,
// aws_s3_bucket.data's identity is parent-derived from random_string.suffix,
// and random_string.suffix is record-backed - which is
// data.aws_eks_cluster.cluster, module.eks.aws_eks_cluster.this[0] and
// random_string.suffix, one for one.
//
// The state file being migrated has had every one of those values the whole
// time, so it is seeded into the fixpoint as prior state. The provider mock
// answers the data source only for the id the state carries, so a run that
// invented the value some other way fails here rather than passing quietly.
func TestLiveImportReadsProviderDataThroughTheStateItIsMigrating(t *testing.T) {
	cloud := importNewCloud()
	cloud.put("aws_s3_bucket", "tofu-import-unit-abc12345",
		map[string]string{"id": "tofu-import-unit-abc12345", "bucket": "tofu-import-unit-abc12345"},
		map[string]string{})
	cloud.put("aws_s3_bucket", "tofu-import-unit-derived",
		map[string]string{"id": "tofu-import-unit-derived", "bucket": "tofu-import-unit-derived"},
		map[string]string{})

	prov := providerDataImportProvider(cloud)
	// The data source answers for exactly the id the state file records for
	// aws_s3_bucket.data, and for nothing else.
	prov.onlyForBucket = "tofu-import-unit-abc12345"

	c, done := newLiveImportCommandIn(t, "live-import-providerdata-record", prov,
		addrs.NewDefaultProvider("random"), providers.FactoryFixed(recordBackedUnitProvider()))
	code := c.Run([]string{"-no-color", "-state=import.tfstate", "-estate=stateless-unit", "-approve"})
	output := done(t)
	stdout, stderr := output.Stdout(), output.Stderr()

	if code != 0 {
		t.Fatalf("exit code %d, want 0\nstdout:\n%s\nstderr:\n%s", code, stdout, stderr)
	}
	if strings.Contains(stdout, "Dynamic value in static context") {
		t.Errorf("the aliased provider was still not configurable:\n%s", stdout)
	}
	if tags := cloud.tagsOf("aws_s3_bucket", "tofu-import-unit-derived"); tags["tofu-estate"] != "stateless-unit" {
		t.Errorf("the resource served by the derived provider was not stamped: tofu-estate = %q\nstdout:\n%s", tags["tofu-estate"], stdout)
	}
	if !prov.configuredWith("us-west-2") {
		t.Errorf("no provider configuration took its region from the data source; configured with %v", prov.regions)
	}
}

// recordBackedUnitProvider serves random_string, the record-backed type whose
// value only the state file has during a migration.
func recordBackedUnitProvider() providers.Interface {
	p := &tofu.MockProvider{
		GetProviderSchemaResponse: &providers.GetProviderSchemaResponse{
			Provider: providers.Schema{Block: &configschema.Block{}},
			ResourceTypes: map[string]providers.Schema{
				"random_string": {Block: &configschema.Block{Attributes: map[string]*configschema.Attribute{
					"id":      {Type: cty.String, Computed: true},
					"result":  {Type: cty.String, Computed: true},
					"length":  {Type: cty.Number, Required: true},
					"special": {Type: cty.Bool, Optional: true},
				}}},
			},
		},
	}
	p.ConfigureProviderCalled = true
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		return providers.ReadResourceResponse{NewState: r.PriorState}
	}
	return p
}
