// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// certificateValidationSchema is aws_acm_certificate_validation's real
// schema (hashicorp/aws 6.59.0, read with `terraform providers schema
// -json` against a real cold-deployed corpus-alb-complete estate, 2026-08-24):
// certificate_arn is the only Required argument, and it is also the whole
// of the type's identity - validation_record_fqdns and timeouts are
// ordinary configuration the provider's own Read never re-derives (id is
// a synthetic create-time timestamp with no semantic meaning at all;
// omitted here because nothing in this package ever needs to set it).
func certificateValidationSchema() providers.Schema {
	return providers.Schema{
		Block: &configschema.Block{
			Attributes: map[string]*configschema.Attribute{
				"certificate_arn": {Type: cty.String, Required: true},
				"validation_record_fqdns": {
					Type:     cty.Set(cty.String),
					Optional: true,
				},
			},
		},
	}
}

// GitHub issue #388's open edge 1: a flag-on run of corpus-alb-complete
// (live/e2e/corpus-alb-complete/run.sh) surfaced a NEW
// "aws_acm_certificate_validation ... Cannot import for projection" error,
// in 2 of 2 flag-on runs and 0 of 3 flag-off runs. Re-verified on an idle
// machine, 3 more times, 3/3 with the error present: not load noise. Root
// cause, traced through the code rather than guessed: this estate's static
// resolution has ALWAYS carried at least one per-instance Error (the two
// aws_lb_target_group_attachment ports and one aws_lambda_permission
// function_name family A/B leave refused, both genuinely - see the script's
// own header), which made identity.Result.HasErrors() true and aborted
// PriorState before projection.BuildWith ever ran, for every measurement
// this estate has ever had. #388's identity.DowngradeForNodeResolution
// turns those same refusals into warnings, so - for the first time - this
// estate's projection actually builds, and it immediately hits a real,
// pre-existing, generic gap this test pins: aws_acm_certificate_validation
// is admitted on nameability alone (identity.Derivable resolves its
// certificate_arn from configuration; tools/row-gen/notimportable.go's own
// notImportableExempt map records, since 2026-08-17, that it has no classic
// Importer either - a fact new evidence surfaced but did not act on,
// because reversing the nameability ruling is the maintainer's call). Once
// a PARENT_DERIVED resolution for such a type reaches materialize(), the
// OLD code asked the provider to classically import it, got back
// "resource ... doesn't support import", and reported "Cannot import for
// projection" - wording that claims the provider is erroring, when it is
// correctly answering a question that will never have a different answer.
//
// The fix: importAndRead recognizes this diagnostic shape (the same two
// substrings tools/survey-gen/schemas.go's probeImportability already uses
// offline) and refuses with an accurate cause instead - same severity, same
// refusal, no risk of a wrong marker or a false create, just an honest
// diagnostic. It reaches every type in the same position, not only the one
// this was found against: tools/row-gen/notimportable.go names
// aws_iam_policy_attachment, aws_iot_ca_certificate and
// aws_lightsail_domain as sharing the same "no classic Importer" fact
// through three different admission routes.
func TestNoClassicImporterRefusesAccuratelyRatherThanClaimingAProviderError(t *testing.T) {
	for _, tc := range []struct {
		name    string
		message string
	}{
		{"sdkv2 legacy wording", "resource aws_acm_certificate_validation doesn't support import"},
		{"plugin framework wording", "Resource Import Not Implemented"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &tofu.MockProvider{}
			p.ConfigureProviderCalled = true
			p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
				var resp providers.ImportResourceStateResponse
				resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("%s", tc.message))
				return resp
			}

			target := providers.ImportTarget{ID: "arn:aws:acm:eu-west-1:000000000000:certificate/deadbeef"}
			_, _, status, diags := importAndRead(t.Context(), p, providers.Schema{}, "aws_acm_certificate_validation", target, target.ID, nil, nil, nil)

			if status != statusFailed {
				t.Fatalf("status = %v, want statusFailed - a type with no classic Importer must still refuse rather than propose a create for an object this run cannot verify", status)
			}
			if !diags.HasErrors() {
				t.Fatal("no error diagnostic raised at all")
			}
			var found *string
			for _, d := range diags {
				if d.Severity() != tfdiags.Error {
					continue
				}
				s := d.Description().Summary
				found = &s
			}
			if found == nil {
				t.Fatal("no error-severity diagnostic found")
			}
			if *found != "Resource type has no classic Importer" {
				t.Errorf("refusal summary = %q, want %q - the old \"Cannot import for projection\" wording claims a transient provider error, which this is not", *found, "Resource type has no classic Importer")
			}
			for _, d := range diags {
				if d.Description().Summary == "Cannot import for projection" {
					t.Error("the generic \"Cannot import for projection\" diagnostic fired alongside the accurate one - noImporterDiagnostics should have short-circuited it")
				}
			}
		})
	}
}

// TestGenuineProviderErrorStillUsesTheGenericRefusal proves
// noImporterDiagnostics does not swallow an ordinary provider failure that
// merely happens to also be an error: only the two documented "no classic
// Importer" wordings are special-cased, so a real, unrelated failure still
// gets the "the provider is erroring" framing that is actually true of it.
func TestGenuineProviderErrorStillUsesTheGenericRefusal(t *testing.T) {
	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var resp providers.ImportResourceStateResponse
		resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("operation error ACM: DescribeCertificate, https response error StatusCode: 500, RequestID: deadbeef, InternalFailure"))
		return resp
	}

	target := providers.ImportTarget{ID: "arn:aws:acm:eu-west-1:000000000000:certificate/deadbeef"}
	_, _, status, diags := importAndRead(t.Context(), p, providers.Schema{}, "aws_acm_certificate_validation", target, target.ID, nil, nil, nil)

	if status != statusFailed {
		t.Fatalf("status = %v, want statusFailed", status)
	}
	var summaries []string
	for _, d := range diags {
		summaries = append(summaries, d.Description().Summary)
	}
	found := false
	for _, s := range summaries {
		if s == "Cannot import for projection" {
			found = true
		}
		if s == "Resource type has no classic Importer" {
			t.Errorf("a genuine 500 was misclassified as \"no classic Importer\": summaries = %v", summaries)
		}
	}
	if !found {
		t.Errorf("expected \"Cannot import for projection\" among %v for a genuine provider error", summaries)
	}
}

// TestNoClassicImporterSynthesizesAStubFromTheResolvedIdentity is the
// record-rung question this package's own #388 unit (7e3eb9e2e3) left open:
// aws_acm_certificate_validation's identity is fully derivable from
// configuration (identity.Derivable resolves certificate_arn), so once
// ImportResourceState answers "no classic Importer", there is no reason to
// stop there rather than build the stub ImportResourceState itself would
// have produced (near-null, with only the identity attribute set) and hand
// it to ReadResource exactly as an ordinarily-imported instance's stub is.
//
// The mock's ReadResourceFn asserts, by value, that PriorState carried
// EXACTLY certificate_arn - nothing else - proving the stub was built from
// identityValues and not from some other source; its NewState is what a
// real DescribeCertificate-backed Read would answer, and the test asserts
// the final materialized value against it by value, not merely that no
// error resulted.
func TestNoClassicImporterSynthesizesAStubFromTheResolvedIdentity(t *testing.T) {
	const arn = "arn:aws:acm:eu-west-1:000000000000:certificate/deadbeef"
	const fqdn = "_c71cf51dad6546803f1aa44141bd8d54.terraform-aws-modules.modules.tf"

	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var resp providers.ImportResourceStateResponse
		resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("resource aws_acm_certificate_validation doesn't support import"))
		return resp
	}
	var priorStateSeen cty.Value
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		priorStateSeen = r.PriorState
		return providers.ReadResourceResponse{
			NewState: cty.ObjectVal(map[string]cty.Value{
				"certificate_arn":         cty.StringVal(arn),
				"validation_record_fqdns": cty.SetVal([]cty.Value{cty.StringVal(fqdn)}),
			}),
		}
	}

	target := providers.ImportTarget{ID: arn}
	identityValues := map[string]string{"certificate_arn": arn}
	obj, _, status, diags := importAndRead(t.Context(), p, certificateValidationSchema(), "aws_acm_certificate_validation", target, arn, identityValues, nil, nil)

	if diags.HasErrors() {
		t.Fatalf("unexpected errors: %v", diags)
	}
	if status != statusMaterialized {
		t.Fatalf("status = %v, want statusMaterialized - a Derivable, NotImportable type with a real identity must not refuse", status)
	}
	if obj == nil {
		t.Fatal("obj is nil despite statusMaterialized")
	}

	// The synthesized stub ReadResource actually saw: certificate_arn set
	// from identityValues, validation_record_fqdns left null exactly as an
	// ImportResourceState stub would have left an attribute it was not
	// given - proving the stub came from identityValues, not a guess.
	if got := priorStateSeen.GetAttr("certificate_arn").AsString(); got != arn {
		t.Errorf("PriorState.certificate_arn = %q, want %q", got, arn)
	}
	if !priorStateSeen.GetAttr("validation_record_fqdns").IsNull() {
		t.Errorf("PriorState.validation_record_fqdns = %#v, want null - nothing in identityValues named it",
			priorStateSeen.GetAttr("validation_record_fqdns"))
	}

	// The materialized value by value, not merely "no error" - HANDOFF's
	// own rule for any change that reclassifies how an instance's identity
	// is filled in.
	if got := obj.Value.GetAttr("certificate_arn").AsString(); got != arn {
		t.Errorf("materialized certificate_arn = %q, want %q", got, arn)
	}
	fqdns := obj.Value.GetAttr("validation_record_fqdns")
	if fqdns.LengthInt() != 1 || fqdns.AsValueSlice()[0].AsString() != fqdn {
		t.Errorf("materialized validation_record_fqdns = %#v, want {%q}", fqdns, fqdn)
	}
}

// TestNoClassicImporterStillRefusesWithNoIdentityValuesToSynthesizeFrom is
// the boundary the synthesis path must not cross: an instance whose
// identity was never resolved to named attribute values - the marker-swept
// or record-located shape identity.LocatedType's own condition 0 already
// keeps a NotImportable type out of, so this should not arise for
// aws_acm_certificate_validation itself in practice, but the function must
// still refuse honestly rather than build an empty stub and guess - keeps
// today's exact "Resource type has no classic Importer" refusal.
//
// Uses the SAME real schema as
// TestNoClassicImporterSynthesizesAStubFromTheResolvedIdentity, changing
// only identityValues to nil, so a future change that made synthesis fire
// on schema presence alone (rather than on having a real identity to place)
// would be caught here rather than by an assertion that happens to have
// stopped exercising the boundary.
func TestNoClassicImporterStillRefusesWithNoIdentityValuesToSynthesizeFrom(t *testing.T) {
	p := &tofu.MockProvider{}
	p.ConfigureProviderCalled = true
	p.ImportResourceStateFn = func(r providers.ImportResourceStateRequest) providers.ImportResourceStateResponse {
		var resp providers.ImportResourceStateResponse
		resp.Diagnostics = resp.Diagnostics.Append(fmt.Errorf("resource aws_acm_certificate_validation doesn't support import"))
		return resp
	}
	p.ReadResourceFn = func(r providers.ReadResourceRequest) providers.ReadResourceResponse {
		t.Fatal("ReadResource must never be called with no identity to synthesize a stub from")
		return providers.ReadResourceResponse{}
	}

	target := providers.ImportTarget{ID: "arn:aws:acm:eu-west-1:000000000000:certificate/deadbeef"}
	_, _, status, diags := importAndRead(t.Context(), p, certificateValidationSchema(), "aws_acm_certificate_validation", target, target.ID, nil, nil, nil)

	if status != statusFailed {
		t.Fatalf("status = %v, want statusFailed", status)
	}
	var found *string
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		s := d.Description().Summary
		found = &s
	}
	if found == nil || *found != "Resource type has no classic Importer" {
		t.Errorf("refusal summary = %v, want \"Resource type has no classic Importer\"", found)
	}
}
