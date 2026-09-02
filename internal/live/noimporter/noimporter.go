// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package noimporter answers one question, generically, for any caller
// that has just asked a provider to import a resource type and been told
// it cannot: is this a provider erroring, or the provider correctly
// answering that ImportResourceState is not implemented for this type at
// all - and if it is the latter, and this run already has a resolved
// identity for the instance, what stub would ImportResourceState itself
// have returned had it existed.
//
// Two callers need exactly this, on two different paths to the same
// provider RPC: internal/live/projection/build.go's pre-walk projection
// (importAndRead, for `choudoufu live-plan`'s stateless report) and
// internal/tofu/node_resource_plan_instance.go's plan-node seam (issue
// #388's ResourceIdentityResolver hook, importState). Neither may import
// the other - internal/tofu must never import the fork's live-mode
// package (see internal/tofu/resource_identity.go's own doc comment: the
// dependency runs the other way), and internal/live/projection already
// imports internal/tofu for unrelated reasons, which would make the
// reverse an import cycle. This package has no dependency on either: only
// providers, cty and tfdiags, the same leaf shape
// internal/live/markers already uses to let internal/tofu/evaluate.go
// import it. Both callers hold the identical classification and
// synthesis logic without either importing the other or the two drifting
// apart by hand-copying it twice.
package noimporter

import (
	"strings"

	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Signals is the same substring pair tools/survey-gen/schemas.go's
// probeImportability already uses to answer identity.NotImportable's
// question offline, one extra ImportResourceState RPC per type: "resource
// ... doesn't support import" is terraform-plugin-sdk/v2's
// helper/schema.Provider.ImportState's own hardcoded text when a
// resource's Importer field is nil, checked before any API call;
// "Resource Import Not Implemented" is terraform-plugin-framework's
// equivalent for a Resource that implements no ImportState method. Both
// are static properties of the provider's own Go code - Importer is nil
// or it is not - never a fact about the account, the region or which
// object was asked for, so a live floci and real AWS answer it
// identically and a run against either learns nothing new by asking
// again with a different identity.
var Signals = []string{
	"doesn't support import",
	"Import Not Implemented",
}

// Diagnostics reports whether every error-severity diagnostic in diags
// matches one of Signals, the same one-strike shape a not-found
// classifier would use for its own signal list. detail is the first
// matching diagnostic's rendered text.
//
// Issue #331's own audit named the population this answers for: a type
// admitted on nameability alone (identity.Derivable resolves it straight
// from configuration, with no discovery and no Importer ever in THAT
// path) can still reach an import attempt once something else lets the
// run reach far enough to try - #388's plan-node seam downgrading a
// sibling instance's static refusal to a warning, for the estate this was
// first found against. tools/row-gen/notimportable.go's own
// notImportableExempt map is the ratified list of such types today
// (aws_acm_certificate_validation is the one this diagnostic shape was
// found against); aws_iam_policy_attachment, aws_iot_ca_certificate and
// aws_lightsail_domain are the same file's own account of who else has no
// classic Importer either, reached by three different admission routes -
// which is why this checks the PROVIDER'S OWN ANSWER rather than any one
// of those rosters: whichever route admitted the type, the provider's
// diagnostic names the same underlying fact once asked.
func Diagnostics(diags tfdiags.Diagnostics) (bool, string) {
	sawError := false
	detail := ""
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		sawError = true
		desc := d.Description()
		text := strings.TrimSpace(desc.Summary + ": " + desc.Detail)
		matched := false
		for _, signal := range Signals {
			if strings.Contains(desc.Summary, signal) || strings.Contains(desc.Detail, signal) {
				matched = true
				break
			}
		}
		if !matched {
			return false, ""
		}
		if detail == "" {
			detail = text
		}
	}
	return sawError, detail
}

// SynthesizeStub builds the stub a caller's ReadResource would otherwise
// have received from providers.Configured.ImportResourceState, for a type
// Diagnostics has just confirmed has no classic Importer at all.
//
// values is one string per identity attribute, keyed by the provider's
// own name for it - build.go's identity.Resolution.IdentityValues or a
// rendered Formula's Attrs for the pre-walk projection path, or a
// resolved providers.ImportTarget.Identity object's own attribute values
// for the plan-node seam - already computed by whichever ordinary
// identity-resolution path got this instance to an import attempt with a
// real target in the first place. Nothing here invents an identity of its
// own; it only places values the caller already resolved onto the
// schema's own attribute names, exactly where ImportResourceState's own
// stub would carry them.
//
// Every attribute this cannot place from values - every one values does
// not name, and every one whose value does not convert onto the schema's
// own type for it - is left null, the same as an ImportResourceState stub
// leaves everything but the identity it was given. That is not a claim
// about the object's real value, only that nothing here can do better; a
// caller's own ReadResource call is what fills it in for real, the same
// as it does for every ordinarily-imported instance.
//
// Returns false - build nothing, and let the caller keep its own refusal
// - when schema carries no block to build against, or when values names
// nothing the schema has: an empty stub would tell ReadResource nothing
// ImportResourceState's own answer would not equally have told it nothing
// with, and a refusal is the honest answer for an instance this run
// genuinely has no identity to hand the provider.
func SynthesizeStub(schema providers.Schema, values map[string]string) (cty.Value, bool) {
	if schema.Block == nil || len(values) == 0 {
		return cty.NilVal, false
	}
	attrTypes := schema.Block.ImpliedType().AttributeTypes()
	attrs := make(map[string]cty.Value, len(attrTypes))
	placed := false
	for name, ty := range attrTypes {
		raw, ok := values[name]
		if !ok {
			attrs[name] = cty.NullVal(ty)
			continue
		}
		converted, err := convert.Convert(cty.StringVal(raw), ty)
		if err != nil {
			// Not a string-shaped attribute, or convert.Convert otherwise
			// refuses - left null exactly as an ImportResourceState stub
			// would have left an attribute it was not given either.
			attrs[name] = cty.NullVal(ty)
			continue
		}
		attrs[name] = converted
		placed = true
	}
	if !placed {
		return cty.NilVal, false
	}
	return cty.ObjectVal(attrs), true
}
