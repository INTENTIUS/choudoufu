// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package command

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #284's acceptance (b): the two-pass resolution
// [statelessResolve] performs, and the bound on it.
//
// Every assertion here is on a RENDERED identity or on how many times the
// provider seam was reached, never on "did it refuse". A second pass that
// came back with a plausible-looking ImportID for a record whose name the
// provider will not know until apply would be worse than the refusal it
// replaced, and Blocked() cannot see the difference.

// certPlanningProvider is the AWS provider's behaviour on the one attribute
// this mechanism turns on, and nothing else: PlanResourceChange fills each
// element of domain_validation_options with a KNOWN domain_name, derived from
// domain_name and subject_alternative_names, and leaves the DNS record's own
// name unknown until the certificate is applied.
//
// Measured against the real provider (6.60.0) in internal/live/projection's
// TestPlanInstancesAgainstTheAWSProvider, which asserts both halves, so this
// stub cannot drift into describing a provider that does not exist.
type certPlanningProvider struct {
	providers.Configured

	// plans counts PlanResourceChange calls, and configures counts how many
	// times the seam was asked for a provider at all. The second is the
	// bound: [projection.PlanInstances] resolves each provider configuration
	// once per call, so two calls mean two passes.
	plans      int
	configures int

	// known decides whether the planned domain_validation_options carry a
	// known domain_name. False is the provider that answers with nothing
	// usable, which is how the ratchet below is exercised.
	known bool
}

func (p *certPlanningProvider) GetProviderSchema(context.Context) providers.GetProviderSchemaResponse {
	return providers.GetProviderSchemaResponse{ResourceTypes: map[string]providers.Schema{
		"aws_acm_certificate": {Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"domain_name":       {Type: cty.String, Optional: true},
				"validation_method": {Type: cty.String, Optional: true},
				"arn":               {Type: cty.String, Computed: true},
				"domain_validation_options": {
					Type: cty.Set(cty.Object(map[string]cty.Type{
						"domain_name":           cty.String,
						"resource_record_name":  cty.String,
						"resource_record_type":  cty.String,
						"resource_record_value": cty.String,
					})),
					Computed: true,
				},
			},
		}},
	}}
}

func (p *certPlanningProvider) PlanResourceChange(_ context.Context, req providers.PlanResourceChangeRequest) providers.PlanResourceChangeResponse {
	p.plans++
	obj := req.ProposedNewState.AsValueMap()
	obj["arn"] = cty.UnknownVal(cty.String)

	optionType := cty.Object(map[string]cty.Type{
		"domain_name":           cty.String,
		"resource_record_name":  cty.String,
		"resource_record_type":  cty.String,
		"resource_record_value": cty.String,
	})
	if !p.known {
		obj["domain_validation_options"] = cty.UnknownVal(cty.Set(optionType))
		return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(obj)}
	}
	obj["domain_validation_options"] = cty.SetVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
		"domain_name":           obj["domain_name"],
		"resource_record_name":  cty.UnknownVal(cty.String),
		"resource_record_type":  cty.UnknownVal(cty.String),
		"resource_record_value": cty.UnknownVal(cty.String),
	})})
	return providers.PlanResourceChangeResponse{PlannedState: cty.ObjectVal(obj)}
}

// seamFor wraps one provider as [projection.Providers] and counts how often
// it is asked.
func (p *certPlanningProvider) seam() projection.Providers {
	return projection.ProviderFunc(func(context.Context, addrs.AbsProviderConfig) (providers.Interface, error) {
		p.configures++
		return p, nil
	})
}

// TestStatelessResolveSecondPassClassifiesTheACMShape is the control. Without
// it the two guards below pass because the mechanism is unreachable rather
// than because it is careful.
func TestStatelessResolveSecondPassClassifiesTheACMShape(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", "live-resolve-acm"))
	prov := &certPlanningProvider{known: true}

	result, diags := statelessResolve(t.Context(), cfg, prov.seam(), nil, nil)
	if diags.HasErrors() {
		t.Fatalf("refused with the certificate's planned values in hand: %s", diags.Err())
	}

	var got []string
	for _, res := range result.All() {
		if res.Addr.Resource.Resource.Type != "aws_route53_record" {
			continue
		}
		got = append(got, fmt.Sprintf("%s %s %q cause=%s", res.Addr, res.Class, res.ImportID, res.Cause))
	}
	sort.Strings(got)

	// The rendered value, never Blocked(). NEEDS_DISCOVERY with an EMPTY
	// import ID is the correct answer here: the record's own name is not
	// known until the certificate is applied, so anything else would be a
	// fabricated identity for a real object.
	want := `aws_route53_record.cert_validation["example.com"] NEEDS_DISCOVERY "" cause=SIBLING_APPLY`
	if len(got) != 1 || got[0] != want {
		t.Fatalf("resolved %v, want exactly:\n  %s", got, want)
	}
}

// TestStatelessResolveAsksTheProviderOnce is the bound stated in
// [statelessResolve]'s doc comment, enforced. [projection.PlanInstances]
// resolves each provider configuration once per call, so a second call - a
// third resolution pass - shows up here as a second configure.
func TestStatelessResolveAsksTheProviderOnce(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", "live-resolve-acm"))
	prov := &certPlanningProvider{known: true}

	if _, diags := statelessResolve(t.Context(), cfg, prov.seam(), nil, nil); diags.HasErrors() {
		t.Fatalf("statelessResolve: %s", diags.Err())
	}
	if prov.configures != 1 {
		t.Errorf("the provider seam was reached %d times, want exactly 1 - one retry is the whole bound, "+
			"and a third pass would be handed the identical value map the second was", prov.configures)
	}
	if prov.plans != 1 {
		t.Errorf("PlanResourceChange was called %d times, want 1 - only aws_acm_certificate is unrepeated "+
			"and therefore plannable", prov.plans)
	}
}

// TestStatelessResolveNeverConfiguresAProviderWithNothingToGain is the cost
// bound AND the shape that keeps the #183 cohort safe at this layer. A
// configuration whose refusals name no managed resource block has nothing a
// planned value could settle, so no plugin is started and no plan call is
// made; a naive fixpoint that simply retried on any error would start a
// provider for every refused run there is.
func TestStatelessResolveNeverConfiguresAProviderWithNothingToGain(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", "live-resolve-no-demand"))
	prov := &certPlanningProvider{known: true}

	_, diags := statelessResolve(t.Context(), cfg, prov.seam(), nil, nil)
	if !diags.HasErrors() {
		t.Fatal("a bucket named with uuid() was resolved; an identity that changes every run cannot say which object a block owns")
	}
	if prov.configures != 0 {
		t.Errorf("the provider seam was reached %d times for a refusal no managed value could settle; want 0", prov.configures)
	}
}

// TestStatelessResolveKeepsTheFirstPassWhenTheSecondIsNoBetter is the ratchet.
//
// A second pass is given MORE information, so it ordinarily refuses less - but
// supplying managed results also changes which references resolution treats as
// symbolic, so a reference that took the symbolic formula route on the first
// pass can take the evaluate-and-refuse route on the second. Here the provider
// answers with domain_validation_options wholly unknown, which settles nothing,
// and the first pass's diagnostics must survive verbatim.
func TestStatelessResolveKeepsTheFirstPassWhenTheSecondIsNoBetter(t *testing.T) {
	cfg := statelessTestLoadConfig(t, filepath.Join("testdata", "live-resolve-acm"))

	useless := &certPlanningProvider{known: false}
	_, withProvider := statelessResolve(t.Context(), cfg, useless.seam(), nil, nil)
	if useless.plans != 1 {
		t.Fatalf("the provider was asked to plan %d times; this test is not exercising the second pass at all", useless.plans)
	}

	// The same configuration with no provider at all, which is exactly a
	// first pass and nothing else.
	_, firstOnly := statelessResolve(t.Context(), cfg, nil, nil, nil)

	if !withProvider.HasErrors() {
		t.Fatal("a second pass whose planned value settles nothing produced a clean resolution")
	}
	if got, want := renderDiags(withProvider), renderDiags(firstOnly); !equalStrings(got, want) {
		t.Errorf("the second pass replaced the first pass's refusals with different ones:\n got %v\nwant %v", got, want)
	}
}

func renderDiags(diags tfdiags.Diagnostics) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		out = append(out, fmt.Sprintf("%s: %s", d.Description().Summary, d.Description().Detail))
	}
	sort.Strings(out)
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
