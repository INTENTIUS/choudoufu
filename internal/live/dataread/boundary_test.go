// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tofu"
)

// declaredTypes is what the command layer measures off each provider's own
// GetProviderSchema and hands the analysis as
// [Options.ProviderManagedTypes]: the external provider serves data sources
// and nothing else, the test provider serves a managed type, and the aws
// provider serves the one this fixture declares.
func declaredTypes() map[addrs.Provider]map[string]bool {
	return map[addrs.Provider]map[string]bool{
		addrs.NewDefaultProvider("external"): {},
		addrs.NewDefaultProvider("test"):     {"test_thing": true},
		addrs.NewDefaultProvider("aws"):      {"aws_cloudwatch_log_group": true},
	}
}

// TestIdentityReadNeverReachesALocalExecutionProvider is the fix for the
// CRITICAL half of the 2026-08-21 audit, at the package layer.
//
// The root-output class had a provider boundary from the day it was written.
// The identity class - #179's, the older one, and the one live/LIMITATIONS.md
// measures in thousands of sites rather than a handful - had none at all, so
// this configuration ran ./name.sh during a live-plan.
//
// The assertion is about the SOURCE being refused and about the provider
// never being asked for, in that order: the second is the property that keeps
// plan a pure preview, and the first is what makes the refusal legible
// instead of a mysterious empty answer.
func TestIdentityReadNeverReachesALocalExecutionProvider(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "identity-local-execution"), nil)
	analysis := Analyze(context.Background(), cfg, Options{ProviderManagedTypes: declaredTypes()})

	external, ok := analysis.SourceFor(addrs.RootModule, addrs.Resource{
		Mode: addrs.DataResourceMode, Type: "external", Name: "naming",
	})
	if !ok {
		t.Fatalf("the fixture's local-execution data source was not demanded at all, so this proves nothing; demanded: %v", demandedKeys(analysis))
	}
	if external.Eligible {
		t.Fatalf("a data source whose read runs a local program was classified readable before the plan")
	}
	if external.ReasonSummary != SummaryProviderNotLive {
		t.Errorf("refused under %q, want %q - the boundary that must stop it is the provider one, not an accident of its arguments", external.ReasonSummary, SummaryProviderNotLive)
	}

	// The control, in the same fixture and the same run: the test provider
	// serves managed resource types, so it is an infrastructure provider even
	// though this configuration manages nothing through it, and confining the
	// identity class must not cost it its read. A boundary that refused this
	// too would be a parity regression against stock, which plans it fine.
	zone, ok := analysis.SourceFor(addrs.RootModule, addrs.Resource{
		Mode: addrs.DataResourceMode, Type: "test_zone", Name: "a",
	})
	if !ok {
		t.Fatalf("data.test_zone.a was not demanded; demanded: %v", demandedKeys(analysis))
	}
	if !zone.Eligible {
		t.Fatalf("data.test_zone.a was refused (%s: %s); its provider serves managed resource types, so the identity class must still read it", zone.ReasonSummary, zone.ReasonDetail)
	}

	// The refusal has to be fatal, and it has to arrive before any provider
	// is configured. Both are the point: a fatal refusal naming the data
	// source is strictly better than silently running the program, and a
	// provider never asked for is a program never run.
	provs := &recordingProviders{provider: &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"name":    req.Config.GetAttr("name"),
				"zone_id": cty.StringVal("Z0423220"),
			})}
		},
	}}
	results, diags := Read(context.Background(), cfg, analysis, provs)
	if !diags.HasErrors() {
		t.Fatalf("the identity class read a local-execution data source without refusing; results: %v", keysOf(results))
	}
	if len(provs.asked) != 0 {
		t.Errorf("the phase configured %v before refusing; an ineligible identity demand must refuse before a single provider is started", provs.asked)
	}
	if got := diags.Err().Error(); !contains(got, "data.external.naming") || !contains(got, "hashicorp/external") {
		t.Errorf("the refusal does not name both the data source and its provider, so an operator cannot act on it:\n%s", got)
	}
}

// TestIdentityBoundaryFailsOpenWithoutSchemas pins the deliberate fail-open in
// [Boundary.servesLiveObjects], because a silent fail-open that nobody chose
// is how a boundary becomes decoration.
//
// A caller with no schema for a provider - live-check, every offline
// instrument, a plugin that would not start - cannot tell a data-source-only
// provider from a cloud one, and refusing on that unknown would turn an
// installation problem into a wall of refusals blaming the configuration. It
// is safe because the same missing plugin is what would have to run the
// program: data "external" cannot execute anything through a provider process
// that does not exist.
func TestIdentityBoundaryFailsOpenWithoutSchemas(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "identity-local-execution"), nil)
	analysis := Analyze(context.Background(), cfg, Options{})

	external, ok := analysis.SourceFor(addrs.RootModule, addrs.Resource{
		Mode: addrs.DataResourceMode, Type: "external", Name: "naming",
	})
	if !ok {
		t.Fatalf("data.external.naming was not demanded; demanded: %v", demandedKeys(analysis))
	}
	if !external.Eligible {
		t.Errorf("with no provider schemas the boundary refused anyway (%s); it must fail open, and the command layer - which always has schemas - is where it closes", external.ReasonSummary)
	}
}

// TestLiveProvidersCrossChecksTheProviderSchema is the audit's third finding.
//
// [configs.Module.ProviderForLocalConfig] answers with whatever source address
// a required_providers entry bound the local name to, and checks nothing about
// whether that provider serves the type. Without the cross-check, a
// configuration can bind a local name to the local-execution provider,
// declare a managed block under it, and vote that provider into the live set
// on the strength of a type it does not serve.
func TestLiveProvidersCrossChecksTheProviderSchema(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "aliased-provider-source"), nil)
	external := addrs.NewDefaultProvider("external")

	if live := LiveProviders(cfg, nil); !live[external] {
		t.Fatalf("without the cross-check the alias should still vote external in - if it does not, this fixture no longer reproduces the hole it pins; got %v", live)
	}
	if live := LiveProviders(cfg, declaredTypes()); live[external] {
		t.Errorf("hashicorp/external is in the live set on the strength of test_thing, a type its own schema does not declare")
	}
}

// TestScopeIsCheckedInsideTheRecursion is the audit's fourth finding.
//
// The -target check used to run over the demand ROOTS. classify recurses, so
// a source demanded only as another source's DEPENDENCY was stored by that
// recursion and never passed through the check - and a -target run read it.
func TestScopeIsCheckedInsideTheRecursion(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "scope-recursion"), nil)

	// Everything is in the plan graph except data.test_zone.a, which is
	// reached only as data.test_record.b's dependency.
	outOfScope := addrs.ConfigResource{
		Module:   addrs.RootModule,
		Resource: addrs.Resource{Mode: addrs.DataResourceMode, Type: "test_zone", Name: "a"},
	}
	opts := Options{Scope: func(addr addrs.ConfigResource) bool {
		return addr.String() != outOfScope.String()
	}}

	analysis := AnalyzeRootOutputs(context.Background(), cfg, opts)
	zone, ok := analysis.SourceFor(outOfScope.Module, outOfScope.Resource)
	if !ok {
		t.Fatalf("data.test_zone.a was not classified at all; the dependency edge this pins is gone. demanded: %v", demandedKeys(analysis))
	}
	if zone.Eligible {
		t.Errorf("an out-of-scope data source reached through classify's recursion was classified readable")
	}
	if zone.ReasonSummary != SummaryOutOfScope {
		t.Errorf("refused under %q, want %q", zone.ReasonSummary, SummaryOutOfScope)
	}

	var read []string
	mock := &tofu.MockProvider{
		GetProviderSchemaResponse: testProviderSchema(),
		ConfigureProviderCalled:   true,
		ReadDataSourceFn: func(req providers.ReadDataSourceRequest) providers.ReadDataSourceResponse {
			read = append(read, req.TypeName)
			return providers.ReadDataSourceResponse{State: cty.ObjectVal(map[string]cty.Value{
				"name":    cty.StringVal("example.com."),
				"zone_id": cty.StringVal("Z0423220"),
			})}
		},
	}
	if _, diags := ReadForOutputs(context.Background(), cfg, analysis, &fakeProviders{provider: mock}); diags.HasErrors() {
		t.Fatalf("a scoped read raised errors: %s", diags.Err())
	}
	if len(read) != 0 {
		t.Errorf("the phase read %v on a -target run that leaves data.test_zone.a out of the plan graph; nothing here is readable without it", read)
	}
}

// TestOutOfScopeIsNeverRaisedOnItsOwnAccount is the blast-radius half of the
// same fix, and it is the reason [Source.OutOfScope] is a field rather than
// just another refusal summary.
//
// [Read]'s contract is fatal: every ineligible demanded source raises an
// error and the run stops. A source excluded because this run's -target does
// not cover it must not be one of them - a block the plan graph does not
// contain is not a hole in the identity map, and raising over one would turn
// every -target run into a refusal of the configuration's untargeted half.
//
// What the run may still refuse over is a source that IS in scope and cannot
// be read because its dependency is not, which is what this fixture produces
// and what the assertions below pin: one error, about the dependent, none
// about the out-of-scope source itself.
func TestOutOfScopeIsNeverRaisedOnItsOwnAccount(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "read-order"), nil)
	zoneAddr := addrs.Resource{Mode: addrs.DataResourceMode, Type: "test_zone", Name: "a"}
	out := addrs.ConfigResource{Module: addrs.RootModule, Resource: zoneAddr}

	analysis := Analyze(context.Background(), cfg, Options{
		Scope: func(addr addrs.ConfigResource) bool { return addr.String() != out.String() },
	})
	zone, ok := analysis.SourceFor(addrs.RootModule, zoneAddr)
	if !ok {
		t.Fatalf("data.test_zone.a was not classified; demanded: %v", demandedKeys(analysis))
	}
	if !zone.OutOfScope || zone.Eligible {
		t.Fatalf("data.test_zone.a: OutOfScope=%v Eligible=%v, want true/false", zone.OutOfScope, zone.Eligible)
	}

	provs := &recordingProviders{provider: &tofu.MockProvider{GetProviderSchemaResponse: testProviderSchema()}}
	_, diags := Read(context.Background(), cfg, analysis, provs)
	if len(provs.asked) != 0 {
		t.Errorf("the phase configured %v; nothing here was readable", provs.asked)
	}
	var summaries []string
	for _, d := range diags {
		summaries = append(summaries, d.Description().Summary)
		if d.Description().Summary == SummaryOutOfScope {
			t.Errorf("an out-of-scope source was raised as a run-stopping refusal: %s", d.Description().Detail)
		}
	}
	if len(diags) != 1 {
		t.Errorf("raised %d diagnostics %v, want exactly one - the in-scope dependent that cannot be read", len(diags), summaries)
	}
}

func demandedKeys(a *Analysis) []string {
	out := make([]string, 0, len(a.Demanded()))
	for _, src := range a.Demanded() {
		out = append(out, moduleLabel(src.Module)+" "+src.Resource.String())
	}
	return out
}

func contains(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && indexOf(haystack, needle) >= 0
}

func indexOf(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
