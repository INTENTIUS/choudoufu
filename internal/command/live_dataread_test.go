// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is the command layer's own test for the two data-read call sites,
// [statelessDataReads] and [statelessRootOutputDataReads].
//
// It exists because an adversarial audit on 2026-08-21 found that the older
// of the two - the identity class, and the one live/LIMITATIONS.md measures in
// thousands of sites - handed the read phase the UNRESTRICTED provider seam,
// so an ordinary configuration could get data "external" run during a
// live-plan. internal/live/dataread's own tests were green throughout: they
// test the classification, and the classification had been right all along.
// Nothing tested the wiring.
//
// So every assertion here is about which provider the seam was ASKED FOR, at
// the call site, through the real function. A test that only checked
// eligibility would go green again the day someone reverted the confinement.

// recordingDataReadProviders is the seam both call sites take, with a memory.
// It records every provider configuration asked for and never hands one back,
// because the property under test is that the phase does not ask - a provider
// it does not ask for is a program it cannot run.
type recordingDataReadProviders struct {
	asked []string

	// declared is what [statelessProviders.managedTypesByProvider] measures
	// off each provider's own GetProviderSchema on a real run: the external
	// provider serves data sources and nothing else, and the aws provider
	// serves the managed type this fixture declares.
	declared map[addrs.Provider]map[string]bool

	// provider is handed back for anything the seam does allow through, so
	// the control half of each test can actually read.
	provider providers.Interface
}

func (p *recordingDataReadProviders) ConfiguredProvider(_ context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
	p.asked = append(p.asked, addr.Provider.String())
	return p.provider, nil
}

func (p *recordingDataReadProviders) managedTypesByProvider(context.Context) map[addrs.Provider]map[string]bool {
	return p.declared
}

// regionReadingProvider answers data.aws_region and nothing else. Asking it
// for anything the boundary should have stopped is a test failure rather than
// a wrong answer, which is why it fails loudly instead of returning a value.
type regionReadingProvider struct {
	providers.Configured
	t    *testing.T
	read []string
}

func (p *regionReadingProvider) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return providers.GetProviderSchemaResponse{
		ResourceTypes: map[string]providers.Schema{
			"aws_cloudwatch_log_group": {Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"name": {Type: cty.String, Optional: true},
					"arn":  {Type: cty.String, Computed: true},
				},
			}},
		},
		DataSources: map[string]providers.Schema{
			"aws_region": {Block: &configschema.Block{
				Attributes: map[string]*configschema.Attribute{
					"name": {Type: cty.String, Computed: true},
				},
			}},
		},
	}
}

func (p *regionReadingProvider) ReadDataSource(_ context.Context, req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
	p.read = append(p.read, req.TypeName)
	if req.TypeName != "aws_region" {
		p.t.Errorf("the phase read %q; the only data source it may read here is aws_region", req.TypeName)
	}
	return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
		"name": cty.StringVal("us-east-1"),
	})}
}

func newDataReadSeam(t *testing.T) (*recordingDataReadProviders, *regionReadingProvider) {
	t.Helper()
	prov := &regionReadingProvider{t: t}
	return &recordingDataReadProviders{
		provider: prov,
		declared: map[addrs.Provider]map[string]bool{
			addrs.NewDefaultProvider("external"): {},
			addrs.NewDefaultProvider("aws"):      {"aws_cloudwatch_log_group": true},
		},
	}, prov
}

// TestStatelessDataReadsNeverConfiguresALocalExecutionProvider is the audit's
// first and most serious finding, pinned at the call site it was found in.
//
// The fixture puts data.external's result in an identity-bearing position,
// which is all it takes: identity demand is fatal, so this call site read
// every eligible demanded source, and data "external" was eligible by every
// rule the phase drew except the one this call site did not use.
//
// The refusal is the right outcome rather than a regrettable one. HANDOFF.md's
// "a wrong marker outranks a missing one" ranks a named refusal above a marker
// computed from a program this fork ran during a plan, and the run refuses
// before a single provider process is started.
func TestStatelessDataReadsNeverConfiguresALocalExecutionProvider(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", "live-dataread-local-execution"))
	seam, prov := newDataReadSeam(t)

	results, diags := statelessDataReads(t.Context(), cfg, seam, nil, nil)
	if !diags.HasErrors() {
		t.Fatalf("the identity read class accepted a configuration whose identity is built on a local-execution data source; results: %v", results)
	}
	for _, asked := range seam.asked {
		if asked == addrs.NewDefaultProvider("external").String() {
			t.Errorf("the phase configured %s - the provider whose read runs a program named by its own arguments", asked)
		}
	}
	for _, typeName := range prov.read {
		if typeName == "external" {
			t.Errorf("the phase read the local-execution data source")
		}
	}

	detail := diags.Err().Error()
	for _, want := range []string{"data.external.naming", "hashicorp/external", "aws_cloudwatch_log_group.named"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the refusal does not name %q, so an operator cannot act on it:\n%s", want, detail)
		}
	}
}

// TestStatelessDataReadsStillReadsTheEstatesOwnProvider is the control, and
// without it the test above passes for a phase that reads nothing at all.
//
// It also pins the line this fork chose. Confining the identity class to the
// providers this configuration manages objects THROUGH was measured and
// rejected: it refuses data sources of any provider the estate happens not to
// manage anything with, none of which can run a program, all of which stock
// OpenTofu plans without complaint. data.aws_region here stands for that
// whole population, and it must still be read.
func TestStatelessDataReadsStillReadsTheEstatesOwnProvider(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", "live-dataread-region-only"))
	seam, prov := newDataReadSeam(t)

	results, diags := statelessDataReads(t.Context(), cfg, seam, nil, nil)
	if diags.HasErrors() {
		t.Fatalf("the phase refused a configuration whose only data source is an ordinary cloud read: %s", diags.Err())
	}
	if len(prov.read) != 1 || prov.read[0] != "aws_region" {
		t.Fatalf("read %v, want exactly [aws_region] - the confinement must not cost an ordinary read", prov.read)
	}
	if _, ok := results["data.aws_region.current"]; !ok {
		t.Errorf("data.aws_region.current produced no value; got %v", sortedResultKeys(results))
	}
}

// TestStatelessRootOutputDataReadsNeverConfiguresALocalExecutionProvider is
// the same wiring assertion for the second call site. That one has been
// confined since it was written, so this is a ratchet rather than a fix: it
// fails the day someone hands [dataread.ReadForOutputs] the raw provider pool
// again.
//
// This class is scoped rather than fatal, so nothing is refused: the output
// simply keeps no prior value and renders as "+".
func TestStatelessRootOutputDataReadsNeverConfiguresALocalExecutionProvider(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", "live-dataread-local-execution"))
	seam, prov := newDataReadSeam(t)

	results, diags := statelessRootOutputDataReads(t.Context(), cfg, seam, nil, nil)
	if diags.HasErrors() {
		t.Fatalf("the root-output read class refused a run; it is scoped and must never do that: %s", diags.Err())
	}
	for _, asked := range seam.asked {
		if asked == addrs.NewDefaultProvider("external").String() {
			t.Errorf("the phase configured %s for a root output's prior value", asked)
		}
	}
	for _, typeName := range prov.read {
		if typeName == "external" {
			t.Errorf("the phase read the local-execution data source")
		}
	}
	if _, ok := results["data.external.naming"]; ok {
		t.Errorf("the local-execution data source produced a value")
	}
	if _, ok := results["data.aws_region.current"]; !ok {
		t.Errorf("the readable data source in the same fixture produced no value, so this proves nothing; got %v", sortedResultKeys(results))
	}
}

func sortedResultKeys(m map[string]cty.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
