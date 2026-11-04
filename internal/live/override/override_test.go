// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package override

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// proof is one override's red-then-green case: a fake of the provider's
// planning for the type, and a prior and configured value that differ only
// in the way the override makes equivalent. Off, the fake plans a replace or
// an update; on, it plans no change.
type proof struct {
	schema *configschema.Block
	// forceNew lists the attributes the fake treats as forcing
	// replacement, as the real provider does for this type.
	forceNew []string
	prior    cty.Value
	config   cty.Value

	// check replaces the plan-shaped proof for an override whose behaviour
	// is not "equivalent configuration plans as no change": an Apply hook,
	// a refusal. It must fail with the override off and pass with it on.
	check func(t *testing.T, o Override)
}

// proofs holds one entry per registered override, added by an init in the
// override's own _test.go file so parallel units never edit a shared list.
// [TestEveryOverrideHasAProof] fails for an override without one.
var proofs = map[string]proof{}

// fakeProvider plans the way an SDKv2 provider does for the attributes a
// proof names: a changed force-new attribute requires replacement and makes
// computed attributes unknown, any other change is an update, and no change
// plans the prior state.
type fakeProvider struct {
	providers.Interface
	typeName string
	proof    proof
	calls    int
}

func (f *fakeProvider) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return providers.GetProviderSchemaResponse{ResourceTypes: map[string]providers.Schema{
		f.typeName: {Block: f.proof.schema},
	}}
}

func (f *fakeProvider) PlanResourceChange(_ context.Context, req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	f.calls++
	prior := req.PriorState.AsValueMap()
	proposed := req.ProposedNewState.AsValueMap()
	var replace []cty.Path
	for _, name := range f.proof.forceNew {
		if !prior[name].RawEquals(proposed[name]) {
			replace = append(replace, cty.GetAttrPath(name))
		}
	}
	planned := map[string]cty.Value{}
	for name, v := range proposed {
		planned[name] = v
		if attr := f.proof.schema.Attributes[name]; attr != nil && attr.Computed {
			planned[name] = prior[name]
			if len(replace) > 0 {
				planned[name] = cty.UnknownVal(attr.Type)
			}
		}
	}
	return providers.PlanResourceChangeResponse{
		PlannedState:     cty.ObjectVal(planned),
		RequiresReplace:  replace,
		LegacyTypeSystem: true,
	}
}

func planWith(t *testing.T, o Override, p proof, on bool) providers.PlanResourceChangeResponse {
	t.Helper()
	fake := &fakeProvider{typeName: o.Type, proof: p}
	var inner providers.Interface = fake
	if on {
		inner = Wrap(fake, map[string][]Override{o.Type: {o}})
	}
	return inner.PlanResourceChange(context.Background(), providers.PlanResourceChangeRequest{
		TypeName:         o.Type,
		PriorState:       p.prior,
		ProposedNewState: proposedNewState(p),
		Config:           p.config,
	})
}

// proposedNewState is what OpenTofu proposes: configured values, with
// computed attributes the configuration leaves null taken from prior.
func proposedNewState(p proof) cty.Value {
	prior := p.prior.AsValueMap()
	out := p.config.AsValueMap()
	for name, v := range out {
		if attr := p.schema.Attributes[name]; attr != nil && attr.Computed && v.IsNull() {
			out[name] = prior[name]
		}
	}
	return cty.ObjectVal(out)
}

// TestEveryOverrideHasAProof is red-then-green for every registered
// override: without it the fake provider plans a change, with it the plan is
// the prior state with nothing to replace, and the plan says which override
// did it.
func TestEveryOverrideHasAProof(t *testing.T) {
	for _, o := range All() {
		t.Run(o.Name, func(t *testing.T) {
			p, ok := proofs[o.Name]
			if !ok {
				t.Fatalf("override %q has no entry in proofs", o.Name)
			}
			if p.check != nil {
				p.check(t, o)
				return
			}
			off := planWith(t, o, p, false)
			if len(off.RequiresReplace) == 0 && off.PlannedState.RawEquals(p.prior) {
				t.Fatalf("without the override the fake plans no change, so this proof cannot fail")
			}
			on := planWith(t, o, p, true)
			if on.Diagnostics.HasErrors() {
				t.Fatalf("errors with the override: %s", on.Diagnostics.Err())
			}
			if len(on.RequiresReplace) != 0 {
				t.Errorf("with the override the plan still replaces: %#v", on.RequiresReplace)
			}
			if !on.PlannedState.RawEquals(p.prior) {
				t.Errorf("with the override the plan is not the prior state:\n got %#v\nwant %#v", on.PlannedState, p.prior)
			}
			if !hasWarning(on.Diagnostics, "Provider override applied", o.Name) {
				t.Errorf("the plan does not name the override that changed it: %#v", on.Diagnostics)
			}
		})
	}
}

// TestNoChangeIsLeftAlone checks an override neither rewrites nor announces
// anything when configuration already matches prior state.
func TestNoChangeIsLeftAlone(t *testing.T) {
	for _, o := range All() {
		p, ok := proofs[o.Name]
		if !ok || p.check != nil {
			continue
		}
		p.config = p.prior
		resp := planWith(t, o, p, true)
		if len(resp.Diagnostics) != 0 {
			t.Errorf("%s: diagnostics on an unchanged plan: %#v", o.Name, resp.Diagnostics)
		}
	}
}

var awsProviderAddr = addrs.NewDefaultProvider("aws")

// TestRegistryIsComplete is the registry's guard: every override carries
// every field the table in #2309 needs, so the generated table and the plan
// warning never have a blank to show.
func TestRegistryIsComplete(t *testing.T) {
	if len(All()) == 0 {
		t.Fatal("no overrides registered")
	}
	upstream := regexp.MustCompile(`^[a-z0-9-]+/[a-z0-9-]+#[0-9]+$`)
	for _, o := range All() {
		if !nameRE.MatchString(o.Name) {
			t.Errorf("%q: name is not lower-case words joined by hyphens", o.Name)
		}
		if strings.Count(o.Provider, "/") != 1 {
			t.Errorf("%s: provider %q is not namespace/type", o.Name, o.Provider)
		}
		if o.Type == "" || o.Summary == "" || o.ProvenOn == "" || (o.Plan == nil && o.Apply == nil && len(o.DestroyFromConfig) == 0) {
			t.Errorf("%s: Type, Summary, ProvenOn and a Plan, Apply or DestroyFromConfig hook are all required", o.Name)
		}
		switch o.Approach {
		case RewriteConfig, RewriteDiff, AroundCall, Refuse, HandleItself:
		default:
			t.Errorf("%s: approach %q is not one of A to E", o.Name, o.Approach)
		}
		if o.SDK != SDKv2 && o.SDK != Framework {
			t.Errorf("%s: SDK %q is not SDKv2 or Framework", o.Name, o.SDK)
		}
		if o.Approach == RewriteDiff && o.SDK == Framework {
			t.Errorf("%s: a B rewrite on a Plugin Framework resource is a hard \"Provider produced inconsistent result\" error", o.Name)
		}
		if len(o.StockAlternative) < 40 {
			t.Errorf("%s: StockAlternative must say what stock OpenTofu offers for this problem and why the override is still worth carrying", o.Name)
		}
		for _, ref := range o.AlsoTracks {
			if !strings.HasPrefix(ref, "opentofu/opentofu#") || !upstream.MatchString(ref) {
				t.Errorf("%s: AlsoTracks entry %q is not opentofu/opentofu#number", o.Name, ref)
			}
		}
		if !upstream.MatchString(o.Upstream) {
			t.Errorf("%s: upstream %q is not owner/repo#number", o.Name, o.Upstream)
		}
		if len(o.Requires) == 0 {
			t.Errorf("%s: Requires is empty, so no schema change could ever switch it off", o.Name)
		}
	}
	if got := ForProvider(awsProviderAddr, All()); len(got["aws_security_group_rule"]) == 0 {
		t.Errorf("ForProvider does not match the default-registry aws provider")
	}
	tfio := addrs.Provider{Hostname: "registry.terraform.io", Namespace: "hashicorp", Type: "aws"}
	if got := ForProvider(tfio, All()); len(got["aws_security_group_rule"]) == 0 {
		t.Errorf("ForProvider does not match registry.terraform.io/hashicorp/aws")
	}
}

func TestEnabled(t *testing.T) {
	all := len(All())
	for _, tc := range []struct {
		value   string
		want    int
		wantErr string
	}{
		{"", all, ""},
		{"  ", all, ""},
		{"off", 0, ""},
		{"-aws-security-group-rule-cidr-order", all - 1, ""},
		{"aws-security-group-rule-cidr-order", 0, "is not \"off\" or a -name entry"},
		{"-no-such-override", 0, "no provider override is named"},
	} {
		got, err := Enabled(tc.value)
		if tc.wantErr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("Enabled(%q): error %v, want one containing %q", tc.value, err, tc.wantErr)
			}
			continue
		}
		if err != nil || len(got) != tc.want {
			t.Errorf("Enabled(%q) = %d overrides, %v; want %d", tc.value, len(got), err, tc.want)
		}
	}
}

// TestSchemaMismatchSkips checks an override whose declared attribute has a
// different type in the loaded schema is not applied, and the plan says so.
func TestSchemaMismatchSkips(t *testing.T) {
	o, _ := Lookup("aws-security-group-rule-cidr-order")
	p := proofs[o.Name]
	changed := *p.schema
	changed.Attributes = map[string]*configschema.Attribute{}
	for k, v := range p.schema.Attributes {
		changed.Attributes[k] = v
	}
	changed.Attributes["cidr_blocks"] = &configschema.Attribute{Type: cty.Set(cty.String), Optional: true}
	p.schema = &changed
	resp := planWith(t, o, p, true)
	if len(resp.RequiresReplace) == 0 {
		t.Errorf("the override ran against a schema it was not written for")
	}
	if !hasWarning(resp.Diagnostics, "Provider override skipped", "cidr_blocks is set of string, not list of string") {
		t.Errorf("no skip warning naming the mismatch: %#v", resp.Diagnostics)
	}
}

func TestWrapLeavesUntouchedProvidersAlone(t *testing.T) {
	fake := &fakeProvider{}
	if Wrap(fake, nil) != providers.Interface(fake) {
		t.Fatal("Wrap with no overrides did not return the provider itself")
	}
}

func hasWarning(diags tfdiags.Diagnostics, summary, detailPart string) bool {
	for _, d := range diags {
		desc := d.Description()
		if d.Severity() == tfdiags.Warning && desc.Summary == summary && strings.Contains(desc.Detail, detailPart) {
			return true
		}
	}
	return false
}

// TestGeneratedTableIsCurrent fails when live/overrides.json does not match
// the registry: run `go run ./tools/override-gen`.
func TestGeneratedTableIsCurrent(t *testing.T) {
	want, err := TableJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join("..", "..", "..", TablePath))
	if err != nil {
		t.Fatalf("reading %s: %v (run `go run ./tools/override-gen`)", TablePath, err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s is stale: run `go run ./tools/override-gen`", TablePath)
	}
}
