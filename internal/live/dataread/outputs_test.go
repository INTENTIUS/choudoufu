// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"errors"
	"path/filepath"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// recordingProviders is [fakeProviders] with a memory: it remembers which
// provider configurations were asked for, which is how the negative control
// below proves a provider was never reached rather than merely never read
// from.
type recordingProviders struct {
	provider providers.Interface
	err      error
	asked    []string
}

func (r *recordingProviders) ConfiguredProvider(_ context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error) {
	r.asked = append(r.asked, addr.Provider.String())
	return r.provider, r.err
}

// TestAnalyzeRootOutputsFindsWhatOutputsReach is the positive half of GitHub
// issue #349's sub-problem 2, at the demand layer: a data source that NO
// identity needs, reached only from a root output's value through a module
// output, is demanded and classified readable.
//
// corpus-lambda-simple's lambda_function_arn_static is exactly this shape -
// a root output forwarding a module output whose expression interpolates
// three data sources nothing else reads - and it rendered as newly created
// on every single live-plan run because the identity-driven demand probe
// (Analyze) never had a reason to ask for them.
func TestAnalyzeRootOutputsFindsWhatOutputsReach(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "root-output-data"), nil)
	analysis := AnalyzeRootOutputs(context.Background(), cfg, Options{})

	if !analysis.Scoped() {
		t.Fatalf("a root-output analysis must be scoped: its refusals cost one output, not the run")
	}

	got := map[string]*Source{}
	for _, src := range analysis.Demanded() {
		got[moduleLabel(src.Module)+" "+src.Resource.String()] = src
	}
	if len(got) != 2 {
		t.Fatalf("demanded %d sources, want 2 (data.test_zone.current in module.m and data.external.archive in the root); got %v", len(got), sortedSourceKeys(got))
	}

	zone := got["module.m data.test_zone.current"]
	if zone == nil {
		t.Fatalf("module.m's data.test_zone.current was not demanded; got %v", sortedSourceKeys(got))
	}
	if !zone.Eligible {
		t.Errorf("data.test_zone.current is not eligible (%s: %s), but its arguments are static and its provider manages test_thing.a in this configuration", zone.ReasonSummary, zone.ReasonDetail)
	}

	// The identity-driven class must be unchanged: nothing here demands a
	// data source, because no identity reads one.
	if identityDemand := Analyze(context.Background(), cfg, Options{}); !identityDemand.Empty() {
		t.Errorf("the identity demand class picked up %d sources from a configuration whose identities read none - the two classes must stay separate", len(identityDemand.Demanded()))
	}
}

// TestRootOutputReadNeverReachesALocalExecutionProvider is the negative
// control, and it is the reason this class has a provider boundary at all.
//
// data "external" runs a program named by its own arguments on the machine
// running the plan. Every OTHER eligibility rule this package draws says it
// is readable: its count is absent, its program and query arguments are
// static, its provider block needs no evaluation. What stops it is
// [LiveProviders] - the external provider manages no live object in this
// configuration, so this run is not already reading the live system through
// it, and a read of it would be a new kind of side effect during a plan
// rather than one more read of an API the projection already reads.
//
// The assertion is deliberately about the PROVIDER being asked for, not
// about a value being absent: a value can be absent for a dozen reasons,
// while "the phase never asked for this provider" is the property that keeps
// plan a pure preview.
func TestRootOutputReadNeverReachesALocalExecutionProvider(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "root-output-data"), nil)
	analysis := AnalyzeRootOutputs(context.Background(), cfg, Options{})

	var external *Source
	for _, src := range analysis.Demanded() {
		if src.Resource.Type == "external" {
			external = src
		}
	}
	if external == nil {
		t.Fatalf("the fixture's local-execution data source was not demanded at all, so this control proves nothing; keys: %v", analysis.Demanded())
	}
	if external.Eligible {
		t.Fatalf("a data source whose read runs a local program was classified readable before the plan")
	}
	if external.ReasonSummary != SummaryProviderNotLive {
		t.Errorf("refused under %q, want %q - the boundary that must stop it is the provider one, not an accident of its arguments", external.ReasonSummary, SummaryProviderNotLive)
	}

	var read []string
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			read = append(read, req.TypeName)
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"name":    req.Config.GetAttr("name"),
				"zone_id": cty.StringVal("Z0423220"),
			})}
		},
	}
	provs := &recordingProviders{provider: mock}

	results, diags := ReadForOutputs(context.Background(), cfg, analysis, provs)
	if diags.HasErrors() {
		t.Fatalf("a scoped read raised errors: %s", diags.Err())
	}
	for _, asked := range provs.asked {
		if asked != addrs.NewDefaultProvider("test").String() {
			t.Errorf("the phase asked for provider %s; the only provider this configuration manages live objects through is test", asked)
		}
	}
	for _, typeName := range read {
		if typeName == "external" {
			t.Errorf("the phase read the local-execution data source")
		}
	}
	if len(read) != 1 || read[0] != "test_zone" {
		t.Errorf("read %v, want exactly [test_zone]", read)
	}
	if _, ok := results["module.m.data.test_zone.current"]; !ok {
		t.Errorf("no value under module.m.data.test_zone.current; keys: %v", keysOf(results))
	}
	if _, ok := results["data.external.archive"]; ok {
		t.Errorf("the local-execution data source produced a value")
	}
}

// TestRootOutputReadDoesNotAbortOnAnIneligibleOrFailingSource is the
// blast-radius pin, and it is the difference between this landing and
// landing a parity regression.
//
// [Read]'s contract is fatal by design: an identity built on a value the
// phase could not read plans to create objects that already exist, so it
// refuses the run. Widening demand to root outputs under that same contract
// would have turned "one output shows +" into "live-plan refuses this whole
// estate" for every configuration whose outputs happen to read a data source
// this phase cannot read - corpus-lambda-simple's own data.external among
// them, which is to say: the fix would have broken the estate it was written
// for.
//
// So: an ineligible source raises nothing, a failing read raises a warning
// and not an error, and in both cases every other source is still read and
// the run carries on.
func TestRootOutputReadDoesNotAbortOnAnIneligibleOrFailingSource(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "root-output-data"), nil)
	analysis := AnalyzeRootOutputs(context.Background(), cfg, Options{})

	t.Run("ineligible", func(t *testing.T) {
		mock := &tofu.MockProvider{
			GetProviderSchemaResponse: testProviderSchema(),
			ConfigureProviderCalled:   true,
			ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
				return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
					"name":    req.Config.GetAttr("name"),
					"zone_id": cty.StringVal("Z0423220"),
				})}
			},
		}
		results, diags := ReadForOutputs(context.Background(), cfg, analysis, &fakeProviders{provider: mock})
		if diags.HasErrors() {
			t.Fatalf("an ineligible demanded source raised an error: %s", diags.Err())
		}
		if len(diags) != 0 {
			t.Errorf("an ineligible source raised %d diagnostic(s); it must raise none - the plan's own \"+ name\" line is the report", len(diags))
		}
		if _, ok := results["module.m.data.test_zone.current"]; !ok {
			t.Errorf("the eligible source was not read even though a DIFFERENT source was ineligible; keys: %v", keysOf(results))
		}
	})

	t.Run("read fails", func(t *testing.T) {
		provs := &fakeProviders{err: errors.New("no credentials")}
		results, diags := ReadForOutputs(context.Background(), cfg, analysis, provs)
		if diags.HasErrors() {
			t.Fatalf("a failed root-output read was raised as an error: %s", diags.Err())
		}
		if len(diags) == 0 {
			t.Errorf("a failed read raised nothing at all; it should warn, so the provider's own words are still visible")
		}
		if len(results) != 0 {
			t.Errorf("results %v, want none - the one read there was to do failed", keysOf(results))
		}
	})
}

// TestLiveProvidersIsDerivedNotListed pins the two halves of the boundary's
// derivation, because the whole defensibility of this class rests on them
// being measurements rather than a list of provider names.
func TestLiveProvidersIsDerivedNotListed(t *testing.T) {
	cfg := loadConfigTree(t, filepath.Join("testdata", "root-output-data"), nil)
	live := LiveProviders(cfg, nil)

	if !live[addrs.NewDefaultProvider("test")] {
		t.Errorf("the test provider is not live even though this configuration manages test_thing.a through it")
	}
	if live[addrs.NewDefaultProvider("external")] {
		t.Errorf("the external provider is live; it serves no managed resource type at all, in this or any configuration, which is what excludes it")
	}
	if live[addrs.NewDefaultProvider("local")] {
		t.Errorf("a provider whose only managed types are logical is live; a logical resource's prior state is a record, so no run reads the live system through it")
	}
}

func sortedSourceKeys(m map[string]*Source) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
