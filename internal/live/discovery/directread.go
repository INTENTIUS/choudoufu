// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/staticeval"
	"github.com/intentius/choudoufu/internal/providers"
)

// This file is GitHub issue #1046's fix: the Resource Groups Tagging API's
// search index lags a migration's own IAM tag writes by minutes at volume
// (measured: 104 of 1,655 stamped resources visible 21 minutes after
// migrate finished with zero failures). aws_iam_policy has a native list
// route and the list itself carries no readable marker either
// (iam:ListPolicies returns no tags at all), so [markerIndex.join]'s #266
// rescue - the estate's own GetResources answer - has nothing to join
// against yet, and the declared instance goes unbound: [unreadableMarkerProblem]
// warns and the plan proposes a create the provider rejects with
// EntityAlreadyExists.
//
// # Why a targeted read is possible here and not in general
//
// [markerIndex.join] and [scanTypeMarkerFallback] both need SOMETHING
// already on the wire to match against - a listed object's own identifier,
// or a tagged ARN the estate-wide sweep already fetched. Neither exists
// while the index is lagging. But aws_iam_policy's live ARN is not a
// mystery: IAM mints it deterministically from the account, the resource's
// own `path` (default "/") and its own `name` argument (see
// identity/table_generated.go's row - the same fact that keeps the type
// ServerAssigned rather than composed component-by-component, because the
// provider's identity schema wants the whole ARN as one opaque string, not
// because the ARN's shape is unknown). When `name` (and `path`, if set)
// evaluate statically from configuration alone, this run can compute the
// candidate ARN itself and ask the provider directly what lives there -
// no list, no index, one Import+Read for exactly the address that is
// stuck, never for the account.
//
// # Why a wrong guess costs nothing
//
// The composed ARN is never trusted as identity by itself. It is read once,
// and [directReadFallback] only ever acts on what the live object's OWN
// tags say: carrying this estate's tofu-estate and a tofu-address naming
// this exact instance binds it, exactly as [markerIndex.join] would have;
// anything else - no such object, an object with no marker, an object
// marked for another estate or another address - never binds. The worst a
// wrong guess can do is waste one read or produce a refusal where the
// ordinary warning would have done (see [directReadOutcome]'s unavailable
// case) - never write a wrong marker.
//
// # Why unavailable escalates to a refusal (part 2)
//
// The general population [unreadableMarkerProblem] covers has no cheaper
// way to settle "does this address's live object exist" than waiting for
// the index or scanning the whole account again, so a warning is the
// honest and only affordable answer. This population does have a cheaper
// way, so an inconclusive result from it - the read could not even be
// attempted, or it found a live object that flatly is not this instance's
// - means this run had the tool to find out and still could not, which is
// a materially different (and stronger) signal than "nothing said either
// way". HANDOFF.md's safety rule reads the trade the same way: "a refusal
// is loud and reversible; a wrong marker is silent" - and a create over an
// address the provider will reject anyway is not even a wrong marker, it
// is a guaranteed apply-time failure this run can see coming and say so
// before committing to it.

// directReadType is one ServerAssigned type whose live ARN a service mints
// deterministically from configuration alone, given the account ID this run
// already resolved from an ordinary list. composeARN evaluates whatever
// arguments that requires through mod's static evaluator, the same subset
// [staticeval.Argument] admits everywhere else in this package
// ([scanTypeContentMatch]); ok is false whenever any of them cannot be
// evaluated that way, which [directReadFallback] treats as "cannot attempt
// this", never as a value to guess at.
type directReadType struct {
	composeARN func(ctx context.Context, mod *configs.Module, rc *configs.Resource, accountID string) (string, bool)
}

// directReadTypes is deliberately a small, named, hand-maintained registry
// rather than a rule read off the identity table: the table's own
// ServerAssigned reason text is prose, not a machine-checkable claim that an
// ARN is name-composable, and guessing that from a string match would be
// exactly the kind of inference this package refuses to make elsewhere. A
// type belongs here only once someone has read its ARN format the way
// aws_iam_policy's row's own Reason does (identity/table_generated.go).
//
// aws_iam_role is NOT here despite carrying the same "IAM mints the ARN
// from account and name" shape: when its `name` argument is present in
// configuration, [identity.Resolve] already classifies it ClassConcrete
// (Components: [{Attrs: ["name"], ...}]) and it never reaches this
// package's discovery scan at all - the projection's own
// ImportResourceState/ReadResource pair verifies its marker directly,
// immune to this exact bug (there is no tag INDEX in that path to lag).
// The only way aws_iam_role reaches ClassNeedsDiscovery is
// ServerAssignedIfAbsent - `name` omitted, a name_prefix left to AWS - and
// there configuration states no name to compose a candidate ARN from at
// all, so a direct read could never apply. Confirmed by reading
// identity/table_generated.go's own two rows rather than assumed from the
// issue text; see the fix's own report for that scouting.
var directReadTypes = map[string]directReadType{
	"aws_iam_policy": {composeARN: composeIAMPolicyARN},
}

// composeIAMPolicyARN builds the ARN IAM would have minted for an
// aws_iam_policy from its own `name` (required) and `path` (optional,
// defaults to "/") arguments, exactly as CreatePolicy documents:
// arn:PARTITION:iam::ACCOUNT:policy/PATH/NAME with the leading and
// trailing slashes of PATH collapsed against the literal "/" already
// between "policy" and PATH.
//
// ok is false whenever `name` cannot be evaluated from configuration alone
// (absent, a data source or resource reference, count.index/each.key,
// anything [staticeval.Argument]'s own subset does not admit), or when
// `path` IS SET in configuration but cannot be. The second half matters
// because path is not merely cosmetic - two policies can share a name at
// different paths - so guessing "/" for a path this run cannot verify
// risks the direct read finding a real, unrelated object and refusing
// [directReadForeign] over it where the correct answer might have been
// "create, this address's own policy does not exist yet". Failing closed
// there costs a refusal instead of a silent create; guessing would risk
// exactly the wrong-marker outcome HANDOFF's safety rule forbids.
//
// This never claims to be aws_iam_policy's identity for any purpose beyond
// this one probe - see this file's own package doc comment for why a wrong
// guess here is bounded.
func composeIAMPolicyARN(ctx context.Context, mod *configs.Module, rc *configs.Resource, accountID string) (string, bool) {
	name, why := staticeval.Argument(ctx, mod, rc, "name")
	if why != "" {
		return "", false
	}

	path := "/"
	if attrPresent(rc, "path") {
		p, pathWhy := staticeval.Argument(ctx, mod, rc, "path")
		if pathWhy != "" {
			return "", false
		}
		path = p
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if !strings.HasSuffix(path, "/") {
		path += "/"
	}
	return fmt.Sprintf("arn:aws:iam::%s:policy%s%s", accountID, path, name), true
}

// attrPresent reports whether rc's body sets a top-level argument named
// name at all, with no evaluation attempted - the same PartialContent probe
// [staticeval.Argument] itself opens with, pulled out so a caller can tell
// "absent, so the type's own default applies" apart from "present, but not
// statically known", which need different answers here (see
// [composeIAMPolicyARN]'s own doc comment).
func attrPresent(rc *configs.Resource, name string) bool {
	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: name}},
	})
	if diags.HasErrors() {
		return false
	}
	_, ok := content.Attributes[name]
	return ok
}

// directReadOutcome is what [directReadFallback] decided for one declared,
// otherwise-unbound instance.
type directReadOutcome int

const (
	// directReadSkipped means this fallback does not apply here at all:
	// typeName is not in [directReadTypes], or the ordinary preconditions
	// [unreadableMarkerProblem] itself checks were not met (the index
	// already names this address - the strong form already covers it more
	// precisely - or nothing unreadable was listed this run). The caller
	// falls through to today's unchanged behavior.
	directReadSkipped directReadOutcome = iota

	// directReadBound means a live object was read at the composed
	// candidate identity and it carries this estate's marker for exactly
	// this address. The caller binds it like any other claimant.
	directReadBound

	// directReadAbsent means the read was attempted and completed, and
	// definitively found nothing at the composed identity - the ordinary,
	// unremarkable first-apply state. The caller falls through to today's
	// warning unchanged: this run cannot rule out that some OTHER listed,
	// unreadable object of this type is the one waiting to be found (a
	// renamed policy, say), only that the specific candidate this fallback
	// guessed is not it.
	directReadAbsent

	// directReadForeign means the read found a live object at the composed
	// identity, and it does not carry this estate's marker for this
	// address: no marker at all, another estate's, or another address's.
	// Part 2's refusal.
	directReadForeign

	// directReadUnavailable means the read could not be attempted or could
	// not be completed at all: no per-instance static scope (a count/
	// for_each instance), the name (or a present path) could not be
	// evaluated from configuration, no AWS account ID resolved yet, the
	// provider handle does not support a direct read, or the read itself
	// errored. Part 2's refusal - this run had a cheaper way to settle the
	// question and it did not work, which is a stronger signal than
	// [unreadableMarkerProblem]'s ordinary silence.
	directReadUnavailable
)

// directReadFallbackEnabled gates [directReadFallback]'s own call site in
// [bind], true for every real caller. It exists so directread_test.go can
// produce a RED proof against this fix's OWN mechanism - the call site
// disabled, nothing else - rather than against main, which carries neither
// this file nor the fake cloud's ImportResourceState/ReadResource methods
// these tests need in order to compile at all. Never read from Request or
// from anything outside this package's own tests; it is not a runtime
// feature flag.
var directReadFallbackEnabled = true

// directReadFallback is issue #1046's whole decision: given a declared
// instance that [bind] is about to report as unbound with nothing claiming
// it, decide whether a targeted direct read settles the question before
// [unreadableMarkerProblem] warns.
//
// Gated on exactly the two conditions [unreadableMarkerProblem] itself
// reads (index silent for this address, at least one unreadable object of
// this type this run listed) so this never fires for a genuine greenfield
// instance sharing a type with directReadTypes - see that function's own
// "how" branches, which this mirrors.
func directReadFallback(ctx context.Context, req Request, decl *declared, res *Result, typeName, escaped string, addr addrs.AbsResourceInstance) (directReadOutcome, *claimant, string) {
	drt, ok := directReadTypes[typeName]
	if !ok {
		return directReadSkipped, nil, ""
	}
	if len(req.markers.marksAddress(typeName, escaped)) > 0 {
		// The index already names a resource for this exact address; the
		// strong form of unreadableMarkerProblem already says so precisely,
		// and a direct read would only repeat evidence the index already
		// gave more cheaply.
		return directReadSkipped, nil, ""
	}
	if decl.unreadable[typeName] == 0 {
		// Nothing this run listed of this type was unreadable, so there is
		// no lag to recover from: an ordinary create is correct and this
		// fallback has nothing to add.
		return directReadSkipped, nil, ""
	}

	if addr.Resource.Key != addrs.NoKey {
		return directReadUnavailable, nil, fmt.Sprintf(
			"%s is one instance of a count or for_each block, and evaluating its own name and path arguments needs a per-instance static scope this fallback does not have (the same restriction issue #272's content match holds itself to)", addr)
	}

	modCfg, modOK := identity.ConfigForModule(req.Config, addr.Module)
	if !modOK || modCfg.Module == nil {
		return directReadUnavailable, nil, "the module that declares it could not be found"
	}
	rc, rcOK := modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
	if !rcOK {
		return directReadUnavailable, nil, "its own resource block could not be found in the configuration"
	}

	scan, _ := res.ScanFor(typeName)
	if scan.AccountID == "" {
		return directReadUnavailable, nil, fmt.Sprintf("this run resolved no AWS account ID from %s's own listing to compose a candidate ARN with", typeName)
	}

	candidateARN, composed := drt.composeARN(ctx, modCfg.Module, rc, scan.AccountID)
	if !composed {
		return directReadUnavailable, nil, "its name (or path) argument could not be evaluated from configuration alone"
	}

	tags, taggable, found, readOK := directReadProbe(ctx, req, typeName, candidateARN)
	if !readOK {
		return directReadUnavailable, nil, fmt.Sprintf("reading the live %s at the composed identity %s failed", typeName, candidateARN)
	}

	incrementScanDirectRead(res, typeName)

	if !found {
		log.Printf("[DEBUG] stateless/discovery: direct read of %s at %s (composed for %s) found no live object; a create is correct for this candidate", typeName, candidateARN, addr)
		return directReadAbsent, nil, ""
	}
	if !taggable || tags[TagEstate] != req.Estate {
		log.Printf("[DEBUG] stateless/discovery: direct read of %s at %s (composed for %s) found a live object with no marker for this estate", typeName, candidateARN, addr)
		return directReadForeign, nil, fmt.Sprintf("a live %s exists at %s and does not carry estate %q's ownership marker", typeName, candidateARN, req.Estate)
	}
	raw, corrupt := GatherAddress(tags)
	if corrupt {
		return directReadForeign, nil, fmt.Sprintf("a live %s exists at %s carrying estate %q's marker, but its tofu-address could not be read", typeName, candidateARN, req.Estate)
	}
	gotEscaped := EscapeAddress(raw)
	if !ValidMarkerAddress(gotEscaped) || gotEscaped != escaped || markerTypeOf(gotEscaped) != typeName {
		return directReadForeign, nil, fmt.Sprintf("a live %s exists at %s carrying estate %q's marker for a different address (%s)", typeName, candidateARN, req.Estate, raw)
	}

	log.Printf("[DEBUG] stateless/discovery: direct read bound %s to the live %s at %s; the estate's tag index had not caught up yet", addr, typeName, candidateARN)
	return directReadBound, &claimant{
		importID:     candidateARN,
		identityAttr: "arn",
		identity:     cty.NilVal,
		marker:       raw,
		escaped:      gotEscaped,
		tags:         tags,
	}, ""
}

// directReader is the two provider RPCs this fallback needs, narrowed from
// [providers.Interface] the same way [listclient.Lister] narrows it for
// listing (that type's own doc comment: "callers that satisfy
// providers.Interface have no list protocol behind them") - so a test
// double only has to grow the methods this leg actually calls, and the
// production provider handle (already the full [providers.Interface];
// see internal/command's statelessProviders.ConfiguredProvider) satisfies
// it for free.
type directReader interface {
	ImportResourceState(ctx context.Context, req providers.ImportResourceStateRequest) providers.ImportResourceStateResponse
	ReadResource(ctx context.Context, req providers.ReadResourceRequest) providers.ReadResourceResponse
}

// directReadProbe reads exactly one candidate identity: Import to get a
// stub the provider recognizes, then Read to fill it in for real - the same
// pair every other live confirmation in this fork uses
// (internal/live/projection/build.go's importAndRead,
// internal/live/liveimport/ratify.go), because Import's own contract says
// its state "may not be complete" and this leg needs the real tags, which
// for aws_iam_policy only Read's own ListPolicyTags call fills in.
//
// ok is false whenever the read could not be trusted at all (no provider
// handle able to do this, or either RPC returning an error diagnostic) -
// the caller treats that as [directReadUnavailable], never as evidence
// about the object. found is false only for a clean, error-free "no such
// object": an empty ImportedResources list, or a Read that reports the
// object gone. Any error path is folded into !ok deliberately, even one a
// provider spells like an ordinary not-found (see build.go's own
// notFoundDiagnostics for why some providers do that): telling the two
// apart needs a per-provider signal table this narrow, rarely-exercised
// path does not carry, and treating an ambiguous error as "unavailable"
// fails toward the refusal HANDOFF's safety rule prefers, never toward a
// silent bind.
func directReadProbe(ctx context.Context, req Request, typeName, candidateARN string) (tags map[string]string, taggable, found, ok bool) {
	provider, isDirectReader := req.Provider.(directReader)
	if !isDirectReader {
		return nil, false, false, false
	}

	importResp := provider.ImportResourceState(ctx, providers.ImportResourceStateRequest{
		TypeName: typeName,
		Target:   providers.ImportTarget{ID: candidateARN},
	})
	if importResp.Diagnostics.HasErrors() {
		return nil, false, false, false
	}
	if len(importResp.ImportedResources) == 0 {
		return nil, false, false, true
	}

	imported := importResp.ImportedResources[0]
	readResp := provider.ReadResource(ctx, providers.ReadResourceRequest{
		TypeName:     typeName,
		PriorState:   imported.State,
		Private:      imported.Private,
		ProviderMeta: cty.NullVal(cty.DynamicPseudoType),
	})
	if readResp.Diagnostics.HasErrors() {
		return nil, false, false, false
	}
	if readResp.NewState == cty.NilVal || readResp.NewState.IsNull() {
		return nil, false, false, true
	}

	tags, taggable = markers.TagsOf(readResp.NewState)
	return tags, taggable, true, true
}

// incrementScanDirectRead records one direct-read attempt against typeName's
// scan row (TypeScan.DirectRead) so a run's own call accounting - the
// per-type line an operator or a smoke test reads - sees this leg's cost,
// the same way TypeScan.Joined already surfaces #266's tag join.
func incrementScanDirectRead(res *Result, typeName string) {
	for i := range res.Scans {
		if res.Scans[i].TypeName == typeName {
			res.Scans[i].DirectRead++
			return
		}
	}
}

// directReadRefusalProblem is part 2's refusal: [directReadForeign] and
// [directReadUnavailable] both land here, distinguished only by why, which
// names the specific reason so an operator sees exactly what this run tried
// and where it stopped.
func directReadRefusalProblem(req Request, typeName string, addr addrs.AbsResourceInstance, why string) Problem {
	return Problem{
		Kind:     ProblemDirectReadUnresolved,
		TypeName: typeName,
		Addr:     addr,
		Detail: fmt.Sprintf(
			"Nothing bound to %s, and the estate's tag index holds no marker for this address while this run listed %s resources with unreadable ownership markers. %s's own ARN can be composed from configuration alone, which recovers from exactly this shape (a tag-index lag behind a recent migration) by reading the candidate object directly - but %s, so this run cannot tell whether creating %s is safe, and applying a create the provider may reject is worse than stopping here. Reconcile it with live-import, fix the configuration issue named above, or re-plan once the estate's tag index has caught up, then apply.",
			addr, typeName, addr, why, addr),
	}
}
