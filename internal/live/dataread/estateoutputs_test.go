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
	tf "github.com/intentius/choudoufu/internal/builtin/providers/tf"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// estateOutputsDeclared is what the command layer measures off each
// provider's own schema for the estate-outputs fixture: the builtin terraform
// provider serves terraform_data and nothing else, which is logical, so the
// provider boundary's tier 1 does not admit it on its own. Without this map
// the boundary fails open (see TestIdentityBoundaryFailsOpenWithoutSchemas)
// and the tests below would pass whether or not the exemption exists.
func estateOutputsDeclared() map[addrs.Provider]map[string]bool {
	return map[addrs.Provider]map[string]bool{
		addrs.NewBuiltInProvider("terraform"): {"terraform_data": true},
		addrs.NewDefaultProvider("aws"):       {"aws_cloudwatch_log_group": true},
	}
}

// fakeEstateOutputs answers the builtin provider's terraform_estate_outputs
// read the way internal/command's liveEstateOutputs does once a live run has
// opened its record store, without a store.
type fakeEstateOutputs struct {
	values map[string]cty.Value
	diags  tfdiags.Diagnostics
	asked  []string
}

func (f *fakeEstateOutputs) ReadEstateOutputs(_ context.Context, estate string, names []string) (map[string]cty.Value, tfdiags.Diagnostics) {
	f.asked = append(f.asked, estate)
	if f.diags.HasErrors() {
		return nil, f.diags
	}
	out := make(map[string]cty.Value, len(names))
	for _, n := range names {
		out[n] = f.values[n]
	}
	return out, f.diags
}

// TestEstateOutputsCrossTheProviderBoundary is GitHub issue #1575's
// classification half: terraform_estate_outputs is read through the builtin
// terraform provider, which manages no live object, and the boundary used to
// refuse it under [SummaryProviderNotLive]. It is now a cross-stack source,
// exempt per source the way terraform_remote_state is, and both halves of the
// boundary - the analysis and the command layer's provider seam - admit it.
func TestEstateOutputsCrossTheProviderBoundary(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "estate-outputs-read"), nil)
	declared := estateOutputsDeclared()
	analysis := Analyze(context.Background(), cfg, Options{ProviderManagedTypes: declared})

	src, ok := analysis.SourceFor(addrs.RootModule, dataAddr(EstateOutputsTypeName, "network"))
	if !ok {
		t.Fatalf("data.terraform_estate_outputs.network was not demanded, so this proves nothing; demanded: %v", demandedKeys(analysis))
	}
	if !src.EstateOutputs {
		t.Errorf("the source was not marked as an estate-outputs read")
	}
	if !src.Eligible {
		t.Fatalf("refused (%s: %s); an estate-outputs read with static arguments must be readable before the plan", src.ReasonSummary, src.ReasonDetail)
	}

	builtin := addrs.NewBuiltInProvider("terraform")
	if NewBoundary(cfg, declared, false).Allows(builtin, false) {
		t.Fatalf("the boundary admits the builtin terraform provider with no exemption, so the exemption this test pins is not what admitted the source")
	}
	if !ReadableProviders(cfg, analysis, declared)[builtin] {
		t.Errorf("the command layer's provider seam would not configure the builtin terraform provider for this read")
	}
}

// TestReadEstateOutputsResolvesTheIdentity is the read half: the phase reads
// the source through the builtin provider's real ReadDataSource, which asks
// the run's estate-outputs reader, and resolution then settles the identity
// from the other estate's recorded value.
func TestReadEstateOutputsResolvesTheIdentity(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "estate-outputs-read"), nil)
	analysis := Analyze(context.Background(), cfg, Options{ProviderManagedTypes: estateOutputsDeclared()})

	reader := &fakeEstateOutputs{values: map[string]cty.Value{"vpc_id": cty.StringVal("vpc-0123")}}
	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: tf.NewProviderWithEstateOutputs(reader)})
	if diags.HasErrors() {
		t.Fatalf("read failed: %s", diags.Err())
	}
	if len(reader.asked) != 1 || reader.asked[0] != "network" {
		t.Errorf("the reader was asked for %v, want exactly [network]", reader.asked)
	}
	val, ok := results["data.terraform_estate_outputs.network"]
	if !ok {
		t.Fatalf("no result under data.terraform_estate_outputs.network; keys: %v", keysOf(results))
	}
	if got := val.GetAttr("values").GetAttr("vpc_id"); got.IsNull() || got.AsString() != "vpc-0123" {
		t.Errorf("values.vpc_id = %#v, want \"vpc-0123\"", got)
	}

	res, resDiags := identity.ResolveWith(context.Background(), cfg, identity.Context{DataResults: results})
	if resDiags.HasErrors() {
		t.Fatalf("resolution over the estate-outputs read failed: %s", resDiags.Err())
	}
	addr, addrDiags := addrs.ParseAbsResourceInstanceStr("aws_cloudwatch_log_group.per_network")
	if addrDiags.HasErrors() {
		t.Fatal(addrDiags.Err())
	}
	resolution, ok := res.Get(addr)
	if !ok {
		t.Fatalf("aws_cloudwatch_log_group.per_network did not resolve")
	}
	if want := "/networks/vpc-0123"; resolution.ImportID != want {
		t.Errorf("resolved to %q, want %q", resolution.ImportID, want)
	}
}

// TestReadEstateOutputsRefusalKeepsTheStoresWords: a read the record store
// refuses - here a missing grant - surfaces from this phase under the
// store's own registered summary, not wrapped in [SummaryReadFailed]. The
// store's sentence names the grant to add; burying it behind "the provider
// said" would make the refusal less actionable in this phase than the same
// refusal raised by the plan walk.
func TestReadEstateOutputsRefusalKeepsTheStoresWords(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "estate-outputs-read"), nil)
	analysis := Analyze(context.Background(), cfg, Options{ProviderManagedTypes: estateOutputsDeclared()})

	const detail = "add the read grant for estate network"
	var refusal tfdiags.Diagnostics
	refusal = refusal.Append(tfdiags.Sourceless(tfdiags.Error, projection.SummaryEstateOutputsDenied, detail))
	reader := &fakeEstateOutputs{diags: refusal}

	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: tf.NewProviderWithEstateOutputs(reader)})
	if results != nil {
		t.Fatalf("a refused read still returned results: %v", keysOf(results))
	}
	if !diags.HasErrors() {
		t.Fatalf("a refused estate-outputs read did not refuse the run")
	}
	found := false
	for _, d := range diags {
		switch d.Description().Summary {
		case projection.SummaryEstateOutputsDenied:
			found = true
			if d.Description().Detail != detail {
				t.Errorf("the store's detail was reworded: %q", d.Description().Detail)
			}
		case SummaryReadFailed:
			t.Errorf("the store's refusal was wrapped in the generic read failure: %s", d.Description().Detail)
		}
	}
	if !found {
		t.Fatalf("no %q refusal; got: %s", projection.SummaryEstateOutputsDenied, diags.Err())
	}
}

// TestReadEstateOutputsWithoutAReaderRefuses: a command that never filled
// the holder (any run without a live block) answers with the provider's own
// "needs a live block" refusal, not a value and not a silent skip.
func TestReadEstateOutputsWithoutAReaderRefuses(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "estate-outputs-read"), nil)
	analysis := Analyze(context.Background(), cfg, Options{ProviderManagedTypes: estateOutputsDeclared()})

	results, diags := Read(context.Background(), cfg, analysis, &fakeProviders{provider: tf.NewProvider()})
	if results != nil || !diags.HasErrors() {
		t.Fatalf("a read with no estate-outputs reader did not refuse; results: %v", keysOf(results))
	}
	want := tf.EstateOutputsNeedLiveBlock().Description().Summary
	for _, d := range diags {
		if d.Description().Summary == want {
			return
		}
	}
	t.Fatalf("no %q refusal; got: %s", want, diags.Err())
}

// TestEstateOutputsTypeNameMatchesTheProvider holds this package's copy of
// the data source's type name to the builtin provider's own.
func TestEstateOutputsTypeNameMatchesTheProvider(t *testing.T) {
	if EstateOutputsTypeName != tf.EstateOutputsTypeName {
		t.Fatalf("dataread reads %q as the estate-outputs data source; the provider names it %q", EstateOutputsTypeName, tf.EstateOutputsTypeName)
	}
}

// TestEstateOutputsStayConfinedForRootOutputs pins the other side of the
// exemption: the root-output class does not read terraform_estate_outputs
// before the plan. A root output's prior value is this estate's own recorded
// output; reading the other estate's current record instead made a plan for
// an estate that had never applied show "No changes" for the output
// (internal/command's TestEstateOutputsReadCrossesEstates).
func TestEstateOutputsStayConfinedForRootOutputs(t *testing.T) {
	cfg := loadConfig(t, filepath.Join("testdata", "estate-outputs-read"), nil)
	declared := estateOutputsDeclared()
	analysis := AnalyzeRootOutputs(context.Background(), cfg, Options{ProviderManagedTypes: declared})

	src, ok := analysis.SourceFor(addrs.RootModule, dataAddr(EstateOutputsTypeName, "network"))
	if !ok {
		t.Fatalf("the root output did not demand the source, so this proves nothing; demanded: %v", demandedKeys(analysis))
	}
	if src.Eligible {
		t.Fatalf("the root-output class classified a terraform_estate_outputs source readable before the plan")
	}
	if src.ReasonSummary != SummaryProviderNotLive {
		t.Errorf("refused under %q, want %q", src.ReasonSummary, SummaryProviderNotLive)
	}
	if ReadableProviders(cfg, analysis, declared)[addrs.NewBuiltInProvider("terraform")] {
		t.Errorf("the provider seam would configure the builtin terraform provider for the root-output class")
	}
}
