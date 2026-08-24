// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"testing"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

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
			_, status, diags := importAndRead(t.Context(), p, providers.Schema{}, "aws_acm_certificate_validation", target, target.ID, cty.NilVal, false)

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
	_, status, diags := importAndRead(t.Context(), p, providers.Schema{}, "aws_acm_certificate_validation", target, target.ID, cty.NilVal, false)

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
