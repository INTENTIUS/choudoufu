// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// fakeProviders satisfies [Providers] with one mock plugin, no processes
// and no network - the read phase's seam is exactly one method wide, which
// is what makes this test possible offline.
type fakeProviders struct {
	provider providers.Interface
	err      error
}

func (f *fakeProviders) ConfiguredProvider(_ context.Context, _ addrs.AbsProviderConfig) (providers.Interface, error) {
	return f.provider, f.err
}

// testProviderSchema serves the two fixture data source types.
func testProviderSchema() *providers.GetProviderSchemaResponse {
	return &providers.GetProviderSchemaResponse{
		Provider: providers.Schema{Block: &configschema.Block{}},
		DataSources: map[string]providers.Schema{
			"test_zone": {Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"name":    {Type: cty.String, Optional: true},
					"zone_id": {Type: cty.String, Computed: true},
				},
			}},
			"test_record": {Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"zone":   {Type: cty.String, Optional: true},
					"id":     {Type: cty.String, Computed: true},
					"secret": {Type: cty.String, Computed: true, Sensitive: true},
				},
			}},
		},
		ResourceTypes: map[string]providers.Schema{},
	}
}

// TestReadOrdersAndFeedsDependencies is the phase's core mechanics in one
// run: topological order between the two reads, the first read's answer
// present in the second read's config, and the results shaped for
// resolution - which then resolves the exact identity, closing the loop
// the design promises.
func TestReadOrdersAndFeedsDependencies(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "read-order"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})
	if got := len(analysis.Demanded()); got != 2 {
		t.Fatalf("demanded %d sources, want 2", got)
	}

	var readOrder []string
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			readOrder = append(readOrder, req.TypeName)
			switch req.TypeName {
			case "test_zone":
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"name":    req.Config.GetAttr("name"),
					"zone_id": cty.StringVal("Z0423220"),
				})}
			case "test_record":
				// The dependency's answer must already be in this config:
				// that is what "read in topological order" buys.
				if got := req.Config.GetAttr("zone"); got.IsNull() || got.AsString() != "Z0423220" {
					t.Errorf("test_record was read with zone %#v, want the zone_id test_zone answered", got)
				}
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"zone":   req.Config.GetAttr("zone"),
					"id":     cty.StringVal("rec-77"),
					"secret": cty.StringVal("hunter2"),
				})}
			}
			t.Fatalf("read of unexpected type %q", req.TypeName)
			return providers.ReadDataSourceResponse{}
		},
	}

	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	if len(readOrder) != 2 || readOrder[0] != "test_zone" || readOrder[1] != "test_record" {
		t.Fatalf("read order %v, want [test_zone test_record]", readOrder)
	}
	if _, ok := results["data.test_zone.a"]; !ok {
		t.Fatalf("no result under data.test_zone.a; keys: %v", keysOf(results))
	}

	// The loop closes: resolution consumes the results and computes the
	// exact identity from the provider's answer.
	res, resDiags := identity.ResolveWith(context.Background(), cfg, identity.Context{DataResults: results})
	if resDiags.HasErrors() {
		t.Fatalf("resolution over the read results failed: %s", resDiags.Err())
	}
	addr, addrDiags := addrs.ParseAbsResourceInstanceStr("aws_cloudwatch_log_group.per_record")
	if addrDiags.HasErrors() {
		t.Fatal(addrDiags.Err())
	}
	resolution, ok := res.Get(addr)
	if !ok {
		t.Fatalf("aws_cloudwatch_log_group.per_record did not resolve")
	}
	if want := "/records/rec-77"; resolution.ImportID != want {
		t.Errorf("resolved to %q, want %q", resolution.ImportID, want)
	}
}

// TestReadSensitiveAttributeStaysRefusedInIdentity: the provider's schema
// marks ride the read value, so a sensitive attribute reaching an identity
// argument refuses - markers are cloud tags, and writing a secret into one
// is disclosure.
func TestReadSensitiveAttributeStaysRefusedInIdentity(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "read-order"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			switch req.TypeName {
			case "test_zone":
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"name":    cty.StringVal("example.com."),
					"zone_id": cty.StringVal("Z0423220"),
				})}
			default:
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"zone":   cty.StringVal("Z0423220"),
					"id":     cty.StringVal("rec-77"),
					"secret": cty.StringVal("hunter2"),
				})}
			}
		},
	}
	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}

	val := results["data.test_record.b"]
	if !val.GetAttr("secret").IsMarked() {
		t.Fatalf("the schema-sensitive attribute came out of the phase unmarked")
	}
}

// tfeProviderSchema serves tfe_outputs the way the real hashicorp/tfe
// provider's schema does: values is the whole-attribute-sensitive map the
// design's "Sensitivity" section names.
func tfeProviderSchema() *providers.GetProviderSchemaResponse {
	return &providers.GetProviderSchemaResponse{
		Provider: providers.Schema{Block: &configschema.Block{}},
		DataSources: map[string]providers.Schema{
			"tfe_outputs": {Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"organization": {Type: cty.String, Optional: true},
					"workspace":    {Type: cty.String, Optional: true},
					"values":       {Type: cty.Map(cty.String), Computed: true, Sensitive: true},
				},
			}},
		},
		ResourceTypes: map[string]providers.Schema{},
	}
}

// TestReadTfeOutputsGoesThroughTheStandardPipeline: an eligible tfe_outputs
// source is read through exactly the same generic readSource path a
// same-stack source is - #179 stage 2 adds an eligibility rule and a
// failure class, not a second read implementation.
func TestReadTfeOutputsGoesThroughTheStandardPipeline(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "tfe-eligible"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	src, ok := analysis.SourceFor(addrs.RootModule, dataAddr("tfe_outputs", "app"))
	if !ok || !src.Eligible {
		t.Fatalf("tfe-eligible fixture did not classify eligible: %+v", src)
	}

	called := false
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: tfeProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			called = true
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"organization": req.Config.GetAttr("organization"),
				"workspace":    req.Config.GetAttr("workspace"),
				"values": cty.MapVal(map[string]cty.Value{
					"log_group": cty.StringVal("prod-app"),
				}),
			})}
		},
	}
	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	if !called {
		t.Fatalf("an eligible tfe_outputs source was never read")
	}

	res, resDiags := identity.ResolveWith(context.Background(), cfg, identity.Context{DataResults: results})
	if resDiags.HasErrors() {
		t.Fatalf("resolution over the tfe_outputs read failed: %s", resDiags.Err())
	}
	addr, addrDiags := addrs.ParseAbsResourceInstanceStr("aws_cloudwatch_log_group.per_workspace")
	if addrDiags.HasErrors() {
		t.Fatal(addrDiags.Err())
	}
	resolution, ok := res.Get(addr)
	if !ok {
		t.Fatalf("aws_cloudwatch_log_group.per_workspace did not resolve")
	}
	if want := "/tfe/prod-app"; resolution.ImportID != want {
		t.Errorf("resolved to %q, want %q", resolution.ImportID, want)
	}
}

// TestReadTfeOutputsSensitiveValueRefusesWithNonsensitiveRemedy: the tfe
// provider marks the whole values attribute sensitive, the mark rides the
// read value into resolution exactly as it does for a same-stack source,
// and the refusal that reaches an identity argument carries the
// nonsensitive() remedy the design calls for.
func TestReadTfeOutputsSensitiveValueRefusesWithNonsensitiveRemedy(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "tfe-sensitive"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: tfeProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"organization": req.Config.GetAttr("organization"),
				"workspace":    req.Config.GetAttr("workspace"),
				"values": cty.MapVal(map[string]cty.Value{
					"log_group": cty.StringVal("prod-app"),
				}),
			})}
		},
	}
	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	if !results["data.tfe_outputs.app"].GetAttr("values").IsMarked() {
		t.Fatalf("tfe_outputs' schema-sensitive values attribute came out of the phase unmarked")
	}

	_, resDiags := identity.ResolveWith(context.Background(), cfg, identity.Context{DataResults: results})
	if !resDiags.HasErrors() {
		t.Fatalf("a sensitive tfe_outputs value reached an identity argument without refusing")
	}
	found := false
	for _, d := range resDiags {
		if d.Description().Summary == "Identity derived from a sensitive value" {
			found = true
			if !strings.Contains(d.Description().Detail, "nonsensitive(") {
				t.Errorf("the refusal lacks the nonsensitive() remedy: %s", d.Description().Detail)
			}
		}
	}
	if !found {
		t.Fatalf("no sensitivity refusal; got: %s", resDiags.Err())
	}
}

// TestReadExpansionFromDependency: a count that needs a dependency's value
// computes at read time, the instances share one read, and resolution sees
// a per-instance tuple.
func TestReadExpansionFromDependency(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "read-expansion"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	reads := 0
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			reads++
			switch req.TypeName {
			case "test_zone":
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"name":    cty.StringVal("example.com."),
					"zone_id": cty.StringVal("Z0423220"),
				})}
			default:
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"zone":   cty.StringVal("Z0423220"),
					"id":     cty.StringVal("rec-77"),
					"secret": cty.NullVal(cty.String),
				})}
			}
		},
	}
	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	if reads != 2 {
		t.Fatalf("%d reads for two data blocks; instances of one block share one read", reads)
	}
	for _, key := range []string{"data.test_record.b[0]", "data.test_record.b[1]"} {
		if _, ok := results[key]; !ok {
			t.Errorf("no result under %s; keys: %v", key, keysOf(results))
		}
	}

	res, resDiags := identity.ResolveWith(context.Background(), cfg, identity.Context{DataResults: results})
	if resDiags.HasErrors() {
		t.Fatalf("resolution over the read results failed: %s", resDiags.Err())
	}
	if got := res.Len(); got != 2 {
		t.Fatalf("%d instances resolved, want 2", got)
	}
}

// TestReadFailureRefusesFatally: the provider's own error is quoted under
// the read-failed class, and no results are returned - a partial map would
// plan to create things that exist.
func TestReadFailureRefusesFatally(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "read-order"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) (resp providers.ReadDataSourceResponse) {
			if req.TypeName == "test_zone" {
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"name":    cty.StringVal("example.com."),
					"zone_id": cty.StringVal("Z0423220"),
				})}
			}
			resp.Diagnostics = resp.Diagnostics.Append(errQuota)
			return resp
		},
	}

	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if results != nil {
		t.Fatalf("a failed read still returned results")
	}
	if !diags.HasErrors() {
		t.Fatalf("a failed read did not refuse")
	}
	found := false
	for _, d := range diags {
		if d.Description().Summary == SummaryReadFailed {
			found = true
			if !strings.Contains(d.Description().Detail, "Throttling: rate exceeded") {
				t.Errorf("the provider's own error is not quoted: %s", d.Description().Detail)
			}
		}
	}
	if !found {
		t.Fatalf("no %q refusal; got: %s", SummaryReadFailed, diags.Err())
	}
}

// TestReadRefusesIneligibleBeforeAnyCall: an ineligible demanded source
// refuses before a single provider call, with the class wording.
func TestReadRefusesIneligibleBeforeAnyCall(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "ineligible-managed"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	called := false
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			called = true
			return providers.ReadDataSourceResponse{}
		},
	}
	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
	if called {
		t.Fatalf("an ineligible analysis still produced a provider call")
	}
	if results != nil || !diags.HasErrors() {
		t.Fatalf("an ineligible demanded source did not refuse")
	}
	if got := diags[0].Description().Summary; got != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", got, SummaryNotReadable)
	}
}

// TestConfigureFailureRefusesWithProviderClass: the analysis passed rule 3
// statically, but the provider's own configure refused at read time - bad
// or missing credentials are the design's example - and the error is
// quoted under the provider class.
func TestConfigureFailureRefusesWithProviderClass(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "read-order"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	_, diags := Read(context.Background(), cfg, analysis, &fakeProviders{err: errNoCreds})
	if !diags.HasErrors() {
		t.Fatalf("a provider that will not configure did not refuse")
	}
	d := diags[0].Description()
	if got := diags[0].Description().Summary; got != SummaryProviderNotConfigurable {
		t.Errorf("refused under %q, want %q", got, SummaryProviderNotConfigurable)
	}
	if !strings.Contains(d.Detail, "no valid credential sources") {
		t.Errorf("the provider's own error is not quoted: %s", d.Detail)
	}
}

var errNoCreds = errCreds{}

type errCreds struct{}

func (errCreds) Error() string { return "no valid credential sources for the provider found" }

var errQuota = errThrottling{}

type errThrottling struct{}

func (errThrottling) Error() string { return "Throttling: rate exceeded" }

func keysOf(m map[string]cty.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
