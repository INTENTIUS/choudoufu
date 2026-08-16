// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
)

// loadConfig loads a fixture directory the way the CLI does, the same
// helper shape identity's tests use.
func loadConfig(t *testing.T, dir string, vars map[string]cty.Value) *configs.Config {
	t.Helper()

	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule,
		hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) {
			if val, ok := vars[v.Name]; ok {
				return val, nil
			}
			if v.Required() {
				return cty.NilVal, hcl.Diagnostics{{
					Severity: hcl.DiagError,
					Summary:  "No value for required variable",
					Detail:   fmt.Sprintf("The root module input variable %q is not set.", v.Name),
					Subject:  v.DeclRange.Ptr(),
				}}
			}
			return v.Default, nil
		},
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

func dataAddr(typeName, name string) addrs.Resource {
	return addrs.Resource{Mode: addrs.DataResourceMode, Type: typeName, Name: name}
}

func analyzeFixture(t *testing.T, name string) *Analysis {
	t.Helper()
	cfg := loadConfig(t, filepath.Join("testdata", name), nil)
	return Analyze(context.Background(), cfg, Options{})
}

// TestAnalyzeDemandIsTransitiveAndDemandDriven: both links of a chain are
// demanded, in dependency order, and a declared-but-unreferenced data
// source is not read at all - demand comes from what identity resolution
// asks for, not from what the configuration declares.
func TestAnalyzeDemandIsTransitiveAndDemandDriven(t *testing.T) {
	a := analyzeFixture(t, "eligible-chain")

	var order []string
	for _, src := range a.Demanded() {
		order = append(order, src.Resource.String())
		if !src.Eligible {
			t.Errorf("%s classified ineligible: %s", src.Resource.String(), src.ReasonDetail)
		}
	}
	want := []string{"data.aws_route53_zone.primary", "data.aws_route53_zone.sub"}
	if len(order) != len(want) {
		t.Fatalf("demanded %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("demanded order %v, want %v (dependencies must come first)", order, want)
		}
	}
	if _, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_region", "unused")); ok {
		t.Errorf("data.aws_region.unused was demanded; nothing identity-bearing references it")
	}
}

// TestAnalyzeManagedReferenceIsIneligible: eligibility rule 4 through the
// arguments, with the class wording naming the managed dependency.
func TestAnalyzeManagedReferenceIsIneligible(t *testing.T) {
	a := analyzeFixture(t, "ineligible-managed")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "of_instance"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("a data source reading a managed resource classified eligible")
	}
	if src.ReasonSummary != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
	}
	for _, part := range []string{"needed to resolve the identity of", "managed resource", "cannot be read before the plan"} {
		if !strings.Contains(src.ReasonDetail, part) {
			t.Errorf("the wording lacks %q: %s", part, src.ReasonDetail)
		}
	}
}

// TestAnalyzeDependsOnManagedIsIneligible: rule 4's depends_on half.
func TestAnalyzeDependsOnManagedIsIneligible(t *testing.T) {
	a := analyzeFixture(t, "ineligible-depends-on")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "gated"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("a data source with depends_on naming a managed resource classified eligible")
	}
	if src.ReasonSummary != SummaryNotReadable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryNotReadable)
	}
	if !strings.Contains(src.ReasonDetail, "depends_on") {
		t.Errorf("the wording does not name depends_on: %s", src.ReasonDetail)
	}
}

// TestAnalyzeNonStaticProviderIsIneligible: rule 3, the ConfiguredProvider
// line, classified offline.
func TestAnalyzeNonStaticProviderIsIneligible(t *testing.T) {
	a := analyzeFixture(t, "ineligible-provider")

	src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", "primary"))
	if !ok {
		t.Fatalf("the demanded data source was not classified at all")
	}
	if src.Eligible {
		t.Fatalf("a data source whose provider block reads a resource classified eligible")
	}
	if src.ReasonSummary != SummaryProviderNotConfigurable {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryProviderNotConfigurable)
	}
	if !strings.Contains(src.ReasonDetail, "provider.aws") {
		t.Errorf("the wording does not name the provider configuration: %s", src.ReasonDetail)
	}
}

// TestAnalyzeCycleIsIneligible: a data-to-data cycle terminates and both
// members refuse rather than looping.
func TestAnalyzeCycleIsIneligible(t *testing.T) {
	a := analyzeFixture(t, "ineligible-cycle")

	for _, name := range []string{"a", "b"} {
		src, ok := a.SourceFor(addrs.RootModule, dataAddr("aws_route53_zone", name))
		if !ok {
			// Only the demanded member of the cycle is guaranteed a record;
			// b is demanded through a.
			if name == "a" {
				t.Fatalf("data.aws_route53_zone.a was not classified")
			}
			continue
		}
		if src.Eligible {
			t.Fatalf("data.aws_route53_zone.%s is in a cycle and classified eligible", name)
		}
		if src.ReasonSummary != SummaryNotReadable {
			t.Errorf("%s refused under %q, want %q", name, src.ReasonSummary, SummaryNotReadable)
		}
	}
}

// TestAnalyzeEmptyForConfigurationsWithoutDataDemand: the phase costs
// nothing for a configuration that never needed it.
func TestAnalyzeEmptyForConfigurationsWithoutDataDemand(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("..", "identity", "testdata", "count-length"), nil)
	a := Analyze(context.Background(), cfg, Options{})
	if !a.Empty() {
		t.Fatalf("a configuration with no data sources produced demand: %v", a.Demanded())
	}
	if res, diags := Read(context.Background(), cfg, a, nil); res != nil || diags.HasErrors() {
		t.Fatalf("Read on an empty analysis did something: %v, %s", res, diags.Err())
	}
}
