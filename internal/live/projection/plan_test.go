// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"path/filepath"
	"testing"

	version "github.com/hashicorp/go-version"
	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
)

// planStub answers GetProviderSchema and PlanResourceChange and nothing
// else. The embedded interface is nil on purpose: any other method this
// package starts calling panics the test rather than returning a zero value
// that looks like an answer.
//
// It serves its own schemas because [PlanInstances] takes them from the
// provider it is about to call rather than from a map merged across every
// provider a configuration uses - the process that decodes a block and the
// process that plans it have to be one process.
type planStub struct {
	providers.Configured
	calls   int
	schemas map[string]providers.Schema
	plan    func(providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse
}

func (s *planStub) PlanResourceChange(_ context.Context, req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	s.calls++
	return s.plan(req)
}

func (s *planStub) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	schemas := s.schemas
	if schemas == nil {
		schemas = map[string]providers.Schema{"stub_cert": stubCertSchema()}
	}
	return providers.GetProviderSchemaResponse{ResourceTypes: schemas}
}

// anyProvider is a [Providers] that answers every address with one instance.
// It is what a test that is not about provider SELECTION wants;
// TestPlanInstancesPlansThroughEachBlocksOwnProvider is the one that is, and
// it never uses this.
func anyProvider(p providers.Interface) Providers {
	return ProviderFunc(func(context.Context, addrs.AbsProviderConfig) (providers.Interface, error) {
		return p, nil
	})
}

// stubCertSchema is the shape under test: an argument the configuration
// states, and a computed attribute a provider derives from it. No provider
// name appears here - the behaviour is what matters, and every provider that
// derives a value at plan time has it.
func stubCertSchema() providers.Schema {
	return providers.Schema{Block: &configschema.Block{
		Attributes: map[string]*configschema.Attribute{
			"names":   {Type: cty.List(cty.String), Optional: true},
			"derived": {Type: cty.List(cty.String), Computed: true},
			"id":      {Type: cty.String, Computed: true},
		},
	}}
}

// derivingStub fills `derived` from `names` the way the AWS provider fills
// domain_validation_options from domain_name and subject_alternative_names:
// during the plan, from arguments alone, with no cloud call.
func derivingStub() *planStub {
	return &planStub{plan: func(req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		obj := req.ProposedNewState.AsValueMap()
		var out []cty.Value
		names := obj["names"]
		if names.IsKnown() && !names.IsNull() {
			for _, n := range names.AsValueSlice() {
				out = append(out, cty.StringVal("_validation."+n.AsString()))
			}
		}
		if len(out) == 0 {
			obj["derived"] = cty.ListValEmpty(cty.String)
		} else {
			obj["derived"] = cty.ListVal(out)
		}
		obj["id"] = cty.UnknownVal(cty.String)
		return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(obj)}
	}}
}

// TestPlanInstancesReturnsWhatTheProviderDerives is the carrier: a value no
// schema states and the configuration cannot fold, obtained by asking the
// provider the same question stock OpenTofu asks it.
func TestPlanInstancesReturnsWhatTheProviderDerives(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-computed")
	stub := derivingStub()

	got, diags := PlanInstances(context.Background(), cfg, anyProvider(stub))
	if diags.HasErrors() {
		t.Fatalf("PlanInstances: %s", diags.Err())
	}

	val, ok := got["stub_cert.cert"]
	if !ok {
		t.Fatalf("stub_cert.cert absent; got keys %v", keysOf(got))
	}
	derived := val.GetAttr("derived")
	if derived.LengthInt() != 2 {
		t.Fatalf("derived has %d elements, want 2 - one per name the configuration states", derived.LengthInt())
	}
	want := []string{"_validation.example.com", "_validation.www.example.com"}
	for i, elem := range derived.AsValueSlice() {
		if elem.AsString() != want[i] {
			t.Errorf("derived[%d] = %q, want %q", i, elem.AsString(), want[i])
		}
	}
}

// TestPlanInstancesSkipsRepeatedResources pins the refusal that keeps this
// honest. A counted resource has one planned value PER INSTANCE, and the key
// set is exactly what a caller is trying to learn; answering with one
// instance's values for all of them is a wrong value, not a missing one.
func TestPlanInstancesSkipsRepeatedResources(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-computed")
	stub := derivingStub()

	got, _ := PlanInstances(context.Background(), cfg, anyProvider(stub))

	if _, ok := got["stub_cert.repeated"]; ok {
		t.Error("stub_cert.repeated was planned; a repeated resource has one value per instance " +
			"and planning \"the\" resource answers for all of them with one instance's values")
	}
	if stub.calls != 1 {
		t.Errorf("provider was asked %d times, want 1 - only the unrepeated resource is plannable", stub.calls)
	}
}

// TestPlanInstancesOmitsWhatTheProviderDeclines is the safety property. A
// provider that errors is answering, and the answer is "no": the resource is
// absent from the result and whatever wanted it refuses exactly as it does
// today. The one outcome that must never happen is a fabricated value.
func TestPlanInstancesOmitsWhatTheProviderDeclines(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-computed")
	refusing := &planStub{plan: func(providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
		var resp providers.PlanResourceChangeResponse
		resp.Diagnostics = resp.Diagnostics.Append(errStub{})
		return resp
	}}

	got, diags := PlanInstances(context.Background(), cfg, anyProvider(refusing))

	if diags.HasErrors() {
		t.Error("a provider declining one resource must not fail the whole pass: the caller's " +
			"refusal is already the correct outcome for that resource")
	}
	if len(got) != 0 {
		t.Errorf("got %v, want nothing - a declined plan must contribute no value at all", keysOf(got))
	}
}

// TestPlanInstancesNeedsAProvider pins that the pass is inert without one, so
// every existing caller keeps today's behaviour exactly.
func TestPlanInstancesNeedsAProvider(t *testing.T) {
	cfg := loadConfig(t, "testdata/plan-computed")
	got, diags := PlanInstances(context.Background(), cfg, nil)
	if diags.HasErrors() || len(got) != 0 {
		t.Errorf("with no provider: got %v and errors=%v, want no values and no errors",
			keysOf(got), diags.HasErrors())
	}
}

type errStub struct{}

func (errStub) Error() string { return "the provider cannot compute this" }

func keysOf(m map[string]cty.Value) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestPlanInstancesReachesIntoModules is not a nicety. The resource whose
// computed attribute a for_each reads is usually in a shared module, not
// beside the block reading it - simpleinfra's acm-certificate module is
// called by four separate estates - so a version of this that walked only the
// root would find nothing at all for the case it was written for, and would
// do so silently.
//
// The key must be the ABSOLUTE instance address, because that is what
// identity's own index parses (addrs.ParseAbsResourceInstanceStr). A bare
// "stub_cert.cert" would be filed against a root resource that does not
// exist: a wrong value rather than a missing one.
func TestPlanInstancesReachesIntoModules(t *testing.T) {
	cfg := loadConfigWithModules(t, "testdata/plan-in-module")
	got, diags := PlanInstances(context.Background(), cfg, anyProvider(derivingStub()))
	if diags.HasErrors() {
		t.Fatalf("PlanInstances: %s", diags.Err())
	}

	const want = "module.acm.stub_cert.cert"
	val, ok := got[want]
	if !ok {
		t.Fatalf("no value under %q; got %v", want, keysOf(got))
	}
	if n := val.GetAttr("derived").LengthInt(); n != 1 {
		t.Errorf("derived has %d elements, want 1", n)
	}
	if _, unqualified := got["stub_cert.cert"]; unqualified {
		t.Error("a module resource was also filed under its bare address, which names a root " +
			"resource that does not exist")
	}
}

// loadConfigWithModules is loadConfig for a fixture that calls a local module.
// The shared helper deliberately fails on one, which is right for every test
// that has no business having modules; this file's does, because the case
// under test IS the module boundary.
func loadConfigWithModules(t *testing.T, dir string) *configs.Config {
	t.Helper()
	parser := configs.NewParser(nil)
	call := configs.NewStaticModuleCall(
		addrs.RootModule, hcl.Range{},
		func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
		dir, "default",
	)
	mod, diags := parser.LoadConfigDir(dir, call)
	if diags.HasErrors() {
		t.Fatalf("loading %s: %s", dir, diags.Error())
	}
	cfg, cfgDiags := configs.BuildConfig(context.Background(), mod, configs.ModuleWalkerFunc(
		func(_ context.Context, req *configs.ModuleRequest) (*configs.Module, *version.Version, hcl.Diagnostics) {
			sub := filepath.Join(dir, filepath.FromSlash(req.SourceAddr.String()))
			subCall := configs.NewStaticModuleCall(
				req.Path, hcl.Range{},
				func(v *configs.Variable) (cty.Value, hcl.Diagnostics) { return v.Default, nil },
				sub, "default",
			)
			m, d := parser.LoadConfigDir(sub, subCall)
			return m, version.Must(version.NewVersion("0.0.0")), d
		},
	))
	if cfgDiags.HasErrors() {
		t.Fatalf("building config for %s: %s", dir, cfgDiags.Error())
	}
	return cfg
}
