// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"fmt"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is GitHub issue #289's fix: [resolver.instance] consulted the
// marker on exactly one condition, [TypeIdentity.ServerAssigned], and made
// every other admitted type answer the ADOPTION question - can a stranger's
// unmarked configuration be taken over - even for an estate this fork
// already stamped at create time. internal/live/stamp writes tofu-estate
// and tofu-address onto every taggable managed resource
// (internal/live/stamp/doc.go, "Which resources are taggable"), so a
// migrated estate's copy of any taggable type already carries the marker
// discovery needs; refusing to use it is refusing evidence that is already
// on the resource.
//
// markerFallbackRefusals is the population issue #289 measured, MINUS one
// entry the issue's own list names and this file deliberately withholds -
// see the paragraph below. What remains is every diagnostic Summary
// [resolver.resolveInstance] can raise while composing an identity VALUE
// from configuration. It excludes two further families on purpose, both
// read directly off the issue's own analysis:
//
//   - the count/for_each/expansion family (Non-static count expression,
//     Invalid for_each set/value/key/condition, Sensitive count/for_each,
//     for_each key cannot be recorded as a marker, and their siblings) -
//     these decide WHICH INSTANCES exist and what their keys are, settled
//     in [resolver.expansionFor] before [resolver.resolveInstance] ever
//     reaches the ServerAssigned check, let alone an identity argument. A
//     marker records an already-known instance's ownership; it cannot
//     supply the key set that instance existing depends on.
//   - the "Reference to ..." family - a fact about a DIFFERENT resource or
//     module instance's existence (or the current module tree's), not
//     about how THIS instance's identity is built. See
//     [resolver.markerFallback]'s own doc comment for how the addr-tagged
//     filter keeps these standing even when they fire while resolving a
//     type in [DiscoverableFallbackTypes].
//
// "Non-static identity argument" is withheld, and this is a deliberate
// narrowing of issue #289's own list rather than an oversight matching it.
// internal/live/check's offline loader (load.go's variableValues) answers
// a required root variable with NO value with cty.UnknownVal of the
// variable's declared type - deliberately, so an adoption analysis with no
// tfvars can still say something about the rest of the configuration - and
// [resolver.stringValue]'s !IsWhollyKnown branch cannot tell that unknown
// apart from a genuinely unresolvable one: both raise this exact Summary.
// GitHub issue #183 already drew the line this collides with for a
// different deferral mechanism (DemandedManagedReads / sibling-apply -
// see internal/live/check/manageddemand_test.go's
// TestNoManagedDemandFromAnUnsetVariable): an artifact of the analysis
// loader's own substitution must never be reported as something a LIVE
// read would settle, because live/corpus-manifest.json's whole #183 cohort
// - 177 refused sites - has to stay a visible "you have not supplied this
// input" refusal, not a deferral to some other mechanism that makes the
// tool's reported compatibility look better than a real run with real
// inputs would.
//
// A marker fallback is exactly the same shape of deferral, through a
// different door, and this package has no way today to tell "the value is
// unknown because a live read has not happened yet" apart from "the value
// is unknown because the run's own inputs are incomplete" - the two
// arrive as the identical cty.Unknown. [resolver.markerFallback]'s
// answer-by-marker claim is only true of the first. Withholding this one
// Summary is the conservative side of that uncertainty: it gives up part
// of #289's population (measured: it is the refusal
// TestNoManagedDemandFromAnUnsetVariable's unset-var-identity-arg fixture
// raises) rather than risk reporting "the marker answers this" for a
// configuration that is not the marker's question to answer at all. Making
// this precise - tracing an unresolvable value back to the specific
// variable reference that produced it, the way
// internal/live/check/unsetvars.go's unsetRefsAt already does for
// ATTRIBUTING an existing refusal - is follow-up work, not done here.
//
// Keeping this list separate from refusals.go's registry (rather than a
// field on [Refusal]) is deliberate: every entry here is checked against
// that registry by TestMarkerFallbackRefusalsAreRegistered, which is the
// safety property a shared field would have given for free, without
// forcing every one of refusals.go's other rows to grow a fourth
// positional value they have nothing to say about.
var markerFallbackRefusals = map[string]bool{
	"Ambiguous list-valued identity argument":      true,
	"Circular identity reference":                  true,
	"Empty per-element identity argument":          true,
	"Expression not evaluable here":                true,
	"Identity argument not set":                    true,
	"Identity derived from a sensitive value":      true,
	"Identity derived from an impure function":     true,
	"Identity not resolvable from configuration":   true,
	"Non-string identity argument":                 true,
	"Not an identity attribute":                    true,
	"Null identity argument":                       true,
	"Per-element identity argument not resolvable": true,
	"Unresolvable identity":                        true,
}

// markerFallback is [resolver.instance]'s second chance for an instance
// [resolver.resolveInstance] failed to resolve: if addr's type is
// [DiscoverableFallbackTypes] and every ERROR diagnostic THIS instance
// raised while resolveInstance ran is one of [markerFallbackRefusals], the
// failure is withdrawn and replaced with a [ClassNeedsDiscovery]
// resolution carrying [DiscoveryMarkerFallback] - the identical shape
// [TypeIdentity.ServerAssigned] already returns a few lines above this
// call site in resolveInstance, for the identical reason: a migrated
// estate's marker is the answer regardless of why configuration could not
// build the value.
//
// diagMark is len(r.diags) captured by the caller immediately before
// resolveInstance ran, so r.diags[diagMark:] is exactly what THIS attempt
// raised - the same window [siblingApplyResolution] reads for the same
// reason.
//
// "This instance's own" is decided by [InstanceFailure.Addr], not by
// position in the slice: resolveInstance can recurse into
// [resolver.instance] for a PARENT reference, and any diagnostic that
// nested call raises is tagged with the parent's own address, not addr's
// (see [resolver.errorf] and [resolver.curInstanceAddr]). Such a
// diagnostic is left standing untouched - it is not this instance's claim
// to withdraw, and if the parent genuinely does not exist ("Reference to a
// resource instance that does not exist", tagged to the parent) the whole
// run still refuses on it regardless of what addr itself resolves to. Only
// a diagnostic tagged to addr counts toward the allow-list decision, and
// only those are ever removed.
//
// The decision is fail-closed the same way [siblingApplyResolution]'s
// hardFailed test is: ANY addr-tagged error diagnostic outside the allow
// list, or NO addr-tagged diagnostic at all (nothing here to explain away),
// leaves the instance refused with every diagnostic exactly as
// resolveInstance left it. A warning is never inspected or removed; only
// error diagnostics are fatal to a run ([Resolve]'s own doc comment), so a
// warning beside a converted resolution cannot cause a wrong marker to be
// written.
func (r *resolver) markerFallback(addr addrs.AbsResourceInstance, diagMark int) (Resolution, bool) {
	typeName := addr.Resource.Resource.Type
	if _, ok := DiscoverableFallbackTypes[typeName]; !ok {
		return Resolution{}, false
	}
	if diagMark > len(r.diags) {
		return Resolution{}, false
	}
	mine := r.diags[diagMark:]
	tag := addr.String()

	drop := make(map[int]bool, len(mine))
	found := false
	for i, d := range mine {
		if d.Severity() != tfdiags.Error {
			continue
		}
		fail := tfdiags.ExtraInfo[InstanceFailure](d)
		if fail.Addr != tag {
			// Not this instance's own claim - left standing. See this
			// function's own doc comment.
			continue
		}
		if !markerFallbackRefusals[d.Description().Summary] {
			return Resolution{}, false
		}
		drop[diagMark+i] = true
		found = true
	}
	if !found {
		return Resolution{}, false
	}

	kept := make(tfdiags.Diagnostics, 0, len(r.diags)-len(drop))
	for i, d := range r.diags {
		if drop[i] {
			continue
		}
		kept = append(kept, d)
	}
	r.diags = kept

	return Resolution{
		Addr:  addr,
		Class: ClassNeedsDiscovery,
		Reason: fmt.Sprintf(
			"%s's identity does not fold from its own configuration, but %s is tagged at create time and a live listing can find it, so the tofu-address marker recovers it.",
			addr.String(), typeName),
		Cause: DiscoveryMarkerFallback,
	}, true
}
