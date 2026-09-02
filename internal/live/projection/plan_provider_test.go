// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file answers, against the real AWS provider, the one question every
// argument about issue #187 has turned on and nobody had measured: does
// PlanResourceChange on a create actually fill aws_acm_certificate's
// domain_validation_options with a KNOWN domain_name?
//
// It has to be measured rather than reasoned about. domain_validation_options
// is Computed, and the ordinary SDK rule is that a Computed attribute with no
// prior state plans as unknown, which would make [PlanInstances] useless for
// this shape. The AWS provider does not follow the ordinary rule here - it
// derives each element's domain_name from domain_name and
// subject_alternative_names during the plan - and that behaviour is recorded
// in no schema, no documentation this fork scrapes, and no test until this
// one.
//
// It needs a provider process and NO CLOUD. There is no floci container here
// and no credentials: PlanResourceChange for a create against a provider
// configured with skip_credentials_validation never leaves the machine. That
// is the property that makes the whole phase affordable in live-plan.

// TestPlanInstancesAgainstTheAWSProvider is the carrier for the ACM/Route53
// validation shape that blocks five simpleinfra estates.
//
// What it asserts is the rendered VALUE, not that a plan came back: an empty
// map, or a set whose one element has an unknown domain_name, would leave
// the for_each exactly as unresolvable as it is today while looking like
// success to any assertion phrased as "PlanInstances returned something".
func TestPlanInstancesAgainstTheAWSProvider(t *testing.T) {
	flocitest.Gate(t, "PlanInstances/aws")
	flocitest.RequireBinary(t, terraformBin)
	flocitest.PluginCacheDir(t)

	ctx := context.Background()
	dir := flocitest.CopyFixtureDir(t, filepath.Join("testdata", "plan-acm-validation"))
	flocitest.Run(t, dir, terraformBin, "init", "-input=false", "-no-color")

	provider, _ := launchAWSProvider(t, dir)
	defer func() { _ = provider }()

	if _, ok := provider.(providers.Configured); !ok {
		t.Fatalf("the configured AWS provider does not implement providers.Configured (%T)", provider)
	}

	cfg := loadConfig(t, dir)
	// The fixture declares one provider configuration and no alias, so
	// answering every address with the one launched plugin is exactly what
	// the seam would resolve to. TestPlanInstancesPlansThroughEachBlocksOwnProvider
	// is where the SELECTION is under test; this one is about what the real
	// AWS provider computes.
	got, diags := PlanInstances(ctx, cfg, anyProvider(provider))
	if diags.HasErrors() {
		t.Fatalf("PlanInstances: %s", diags.Err())
	}

	certVal, found := got["aws_acm_certificate.cert"]
	if !found {
		t.Fatalf("the provider produced no planned value for aws_acm_certificate.cert; got keys %v", keysOf(got))
	}

	opts := certVal.GetAttr("domain_validation_options")
	if opts.IsNull() || !opts.IsKnown() {
		t.Fatalf("domain_validation_options planned as %#v, so no key set can be folded from it and PlanInstances cannot serve this shape", opts)
	}

	// The keys the for_each comprehension builds. Every one of them has to
	// be a KNOWN string; an unknown here is the whole refusal, restated.
	var names []string
	for it := opts.ElementIterator(); it.Next(); {
		_, elem := it.Element()
		dn := elem.GetAttr("domain_name")
		if !dn.IsKnown() || dn.IsNull() {
			t.Fatalf("an element of domain_validation_options has domain_name %#v; the for_each key set is still unknowable", dn)
		}
		names = append(names, dn.AsString())
	}
	t.Logf("the AWS provider planned domain_validation_options with %d element(s), domain_name(s) %v", len(names), names)
	if len(names) != 2 {
		t.Errorf("planned %d validation option(s) %v, want one per domain_name/subject_alternative_names entry (2)", len(names), names)
	}

	// The other half of the same value, and the reason the second resolution
	// pass still refuses per instance: the record NAME is unknown until
	// apply. This is not a defect, it is the fact the new discovery cause
	// exists to name, and pinning it here is what stops a later change from
	// quietly fabricating one.
	for it := opts.ElementIterator(); it.Next(); {
		_, elem := it.Element()
		if rr := elem.GetAttr("resource_record_name"); rr.IsKnown() {
			t.Errorf("resource_record_name planned as known (%#v); if the provider now derives it, the route53 records resolve concretely and the discovery cause added for them is dead code", rr)
		}
	}

	// And the end-to-end consequence, which is the only thing a user sees:
	// resolution with these values in hand stops refusing the KEY SET.
	_, idDiags := identity.ResolveWith(ctx, cfg, identity.Context{ManagedResults: got})
	for _, d := range idDiags {
		if d.Description().Summary == "Non-static for_each expression" {
			t.Fatalf("the key set is still refused with the provider's own planned value in hand: %s", d.Description().Detail)
		}
	}
	t.Logf("resolution with planned values: %d diagnostic(s)", len(idDiags))
	for _, d := range idDiags {
		t.Logf("  %s: %s", d.Description().Summary, d.Description().Detail)
	}
}
