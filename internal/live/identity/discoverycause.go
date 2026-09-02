// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

// DiscoveryCause says, structurally, WHY an instance classified
// [ClassNeedsDiscovery]. [Resolution.Reason] has always carried the same
// information as an operator-facing English sentence; this carries it in a
// form a caller can branch on.
//
// It exists because the one downstream consumer that has to act on the
// distinction could not see it. [internal/live/stamp.Request.NeedsDiscovery]
// was a map[string]bool, so stamping could not tell "the provider mints this
// identity and no argument reconstructs it" from "this run was never told
// which AWS account it is pointed at" or from "the configuration left the
// name argument out"; all three produced the one sentence "has an identity
// the provider assigns at create time", and only the first is true. The
// three have different fixes - none, tell the run its account, set the
// argument - and an operator who is handed the wrong one has no way back.
//
// Every value below is derived from the shape of the resolution that
// produced it (the table entry's own fields, or which component failed and
// how), never from a resource type name. A resolution built by something
// other than this package's own walk carries [DiscoveryCauseUnspecified] and
// every reader falls back to the generic wording, which is what all of them
// said before this type existed.
type DiscoveryCause string

const (
	// DiscoveryCauseUnspecified is what a [ClassNeedsDiscovery] resolution
	// carries when nothing established a cause: a resolution assembled by a
	// caller rather than by this package's walk, most often
	// [Resolution.Undeclared] instances that marker discovery adds after the
	// fact.
	//
	// It is deliberately a non-empty string. Readers key a map by resource
	// address to look a cause up, and the empty string is what a Go map
	// returns for a key that is not there at all - "not marker-only" - so an
	// empty cause and an absent one must not be spellable the same way.
	DiscoveryCauseUnspecified DiscoveryCause = "UNSPECIFIED"

	// DiscoveryServerAssigned is [TypeIdentity.ServerAssigned]: the whole
	// type's identity is minted by the provider and no argument in the
	// configuration reconstructs it. There is no edit that makes such an
	// instance resolvable, which is why this is the one cause whose
	// operator-facing sentence offers no next step.
	DiscoveryServerAssigned DiscoveryCause = "SERVER_ASSIGNED"

	// DiscoveryCloudUnknown is a [Component.Cloud] this run was not given:
	// the identity is built from the account or the region the run is
	// pointed at, and nothing told resolution what those are (see
	// [CloudContext]). The identity IS computable - every other component
	// came out of the configuration - so this is a gap in what the run knows
	// rather than a property of the type.
	//
	// [Resolution.CauseArgs] holds the missing [CloudValue] first, then the
	// failing component's [Component.Attrs] - the arguments the provider's
	// own Argument Reference documents as defaulting to that cloud property
	// (a Glue catalog_id defaults to the caller's account; a region
	// argument defaults to the provider's region). Setting one of those in
	// the configuration makes the identity computable with no cloud call
	// and no marker, so it is the operator's way out and a reader must be
	// able to name it. A component with no such argument - a bare account
	// segment in the middle of an ARN, say - contributes no further entries
	// and leaves the sentence with no step to offer, which is true of it.
	//
	// The slice is therefore at least one long for this cause, and index 0
	// is always the cloud property. Readers written against the
	// single-entry shape keep working unchanged.
	DiscoveryCloudUnknown DiscoveryCause = "CLOUD_UNKNOWN"

	// DiscoveryNameOmitted is [Component.ServerAssignedIfAbsent] on an
	// argument the configuration did not set: the provider's own Argument
	// Reference documents that it invents a value when the argument is
	// omitted. Every other component of the identity resolved. Setting the
	// argument to a value the configuration chooses makes the whole identity
	// computable from configuration alone.
	//
	// [Resolution.CauseArgs] holds the candidate argument names, in the
	// order the type's entry lists them.
	DiscoveryNameOmitted DiscoveryCause = "NAME_OMITTED"

	// DiscoveryNamePrefix is the same convention spelled the other way: the
	// configuration names the object through "<base>_prefix", so the
	// provider appends a random suffix at create time. Naming it through
	// <base> instead makes the identity computable.
	//
	// [Resolution.CauseArgs] holds two entries, the base argument name and
	// the prefix argument name actually set.
	DiscoveryNamePrefix DiscoveryCause = "NAME_PREFIX"

	// DiscoveryUniqueName is [TypeIdentity.UniqueName] on a row whose
	// configuration states the name: the identity is still minted by the
	// provider, so the import ID cannot be computed here, but the object can
	// be recognised in a listing without an ownership marker, because AWS
	// itself refuses to issue that name twice.
	//
	// It is a different answer from [DiscoveryServerAssigned], and the
	// difference is the whole reason this cause exists rather than a flag on
	// that one. A server-assigned instance is found by its marker and by
	// nothing else, so applying it unmarked creates an object no later run
	// can ever recognise - which is why internal/live/stamp escalates a
	// missing marker on one to an error. An instance in THIS state is found
	// by its name whether or not a marker was written, so the same missing
	// marker is not that mistake, and stamping must not refuse it. That is
	// what makes an untaggable type of this shape admissible at all.
	//
	// [Resolution.CauseArgs] holds the argument names the name was looked
	// for under, in the order [TypeIdentity.UniqueName] lists them, and
	// [Resolution.UniqueName] holds the value found. An instance whose name
	// argument could not be resolved to a string does NOT reach this cause -
	// it stays [DiscoveryServerAssigned], because a name this run cannot
	// compute is a name no listing can be matched against, and reporting it
	// as bindable would be the wrong-marker outcome this whole mechanism is
	// built to avoid.
	DiscoveryUniqueName DiscoveryCause = "UNIQUE_NAME"

	// DiscoverySiblingApply is GitHub issue #187's answer for an instance
	// whose identity the configuration WOULD determine, except that one of
	// its arguments reads a value the provider does not fill in until
	// another resource in this same configuration has been applied.
	//
	// The carrier is the ACM/Route53 validation pattern:
	//
	//	for_each = { for dvo in aws_acm_certificate.cert.domain_validation_options : ... }
	//	name     = each.value.name
	//
	// Measured against the real AWS provider (internal/live/projection's
	// TestPlanInstancesAgainstTheAWSProvider): PlanResourceChange fills each
	// element's domain_name, so the INSTANCE KEYS are known before anything
	// exists, and leaves resource_record_name unknown, so the record's own
	// name is not.
	//
	// It is deliberately not [DiscoveryServerAssigned], and the difference is
	// not cosmetic. aws_route53_record's identity is ordinarily computable
	// from its own arguments; nothing about the type is server-minted. What
	// is unknown is one argument, this run, because a sibling has not been
	// applied yet - and once that sibling exists, a read of it makes the
	// identity computable with no marker involved. Reporting it as
	// server-assigned would tell the operator there is no way back when the
	// way back is "apply the certificate".
	//
	// [Resolution.CauseArgs] holds the sibling resource block first,
	// module-relative and rendered the way the configuration writes it, then
	// the identity arguments that wait on it, in component order. The slice
	// is therefore at least one long and index 0 is always the sibling.
	DiscoverySiblingApply DiscoveryCause = "SIBLING_APPLY"

	// DiscoveryMarkerFallback is GitHub issue #289's answer: this
	// instance's identity could not be built from its own configuration -
	// a sensitive value, an impure function, a circular reference, an
	// argument left unset, any of the shapes
	// [markerFallbackRefusals] names - but its TYPE is
	// [DiscoverableFallbackTypes]: taggable, and enumerable through a
	// native list resource or a Cloud Control list handler. A migrated
	// estate already carries the tofu-address marker on this instance, so
	// the question configuration just failed to answer is exactly the one
	// the marker answers, the same way it answers
	// [DiscoveryServerAssigned] - the two differ only in WHY configuration
	// could not build the value: a type-level fact for ServerAssigned, one
	// instance's own expression for this cause.
	//
	// It is distinct from DiscoveryServerAssigned because the underlying
	// claim is different and a reader needs to be able to tell them apart:
	// ServerAssigned means no configuration edit could ever help, and this
	// cause means a DIFFERENT instance of the identical resource type,
	// written differently, resolves from configuration alone every day -
	// this one instance's own expression is what did not fold. See
	// [resolver.markerFallback] for the mechanism and
	// internal/live/identity/markerfallback.go for the refusal set it
	// answers.
	DiscoveryMarkerFallback DiscoveryCause = "MARKER_FALLBACK"
)

// AllDiscoveryCauses is every cause this package can produce, in a stable
// order. It exists so a consumer that renders one sentence per cause can be
// tested against the full set rather than against the causes whoever wrote
// the test happened to think of.
func AllDiscoveryCauses() []DiscoveryCause {
	return []DiscoveryCause{
		DiscoveryCauseUnspecified,
		DiscoveryServerAssigned,
		DiscoveryCloudUnknown,
		DiscoveryNameOmitted,
		DiscoveryNamePrefix,
		DiscoveryUniqueName,
		DiscoverySiblingApply,
		DiscoveryMarkerFallback,
	}
}

// BindsByName reports whether an instance with this cause can be recognised
// in a listing without an ownership marker. It is the question every reader
// that has to decide "is a missing marker fatal here" asks, stated once, so
// that adding a further bindable cause does not mean finding every reader
// that spelled the comparison out.
func (c DiscoveryCause) BindsByName() bool {
	return c.Normalize() == DiscoveryUniqueName
}

// Normalize maps the zero value onto [DiscoveryCauseUnspecified] and leaves
// every other cause alone. Consumers that read a cause out of a map must use
// the comma-ok form to decide presence and then call this, so that an entry a
// caller left at the zero value still reads as "marker-only, cause unknown"
// rather than as "not marker-only".
func (c DiscoveryCause) Normalize() DiscoveryCause {
	if c == "" {
		return DiscoveryCauseUnspecified
	}
	return c
}

// Describe renders a cloud property for an operator-facing sentence.
func (c CloudValue) Describe() string { return c.describe() }

// BlockDiscovery is one resource block's marker-only verdict: why every
// instance of it needs discovery, and the subjects that cause is about. It is
// [Resolution.Cause] and [Resolution.CauseArgs] lifted from the instance to
// the block, which is the granularity every consumer of this works at - one
// configuration body serves every instance a count or for_each expands to.
type BlockDiscovery struct {
	Cause DiscoveryCause
	Args  []string
}

// sameAs reports whether two block verdicts say the same thing, which is what
// [Result.DiscoveryCausesByBlock] needs to decide whether a block's instances
// agree.
func (b BlockDiscovery) sameAs(other BlockDiscovery) bool {
	if b.Cause.Normalize() != other.Cause.Normalize() || len(b.Args) != len(other.Args) {
		return false
	}
	for i := range b.Args {
		if b.Args[i] != other.Args[i] {
			return false
		}
	}
	return true
}

// DiscoveryCausesByBlock is the marker-only set every consumer downstream of
// resolution actually wants: the resource BLOCKS whose instances can only be
// found by their ownership marker, each mapped to why.
//
// The key is [addrs.ConfigResource.String] - a module-qualified block
// address with no instance key - because both readers
// ([internal/live/stamp.Request.NeedsDiscovery] and internal/command's
// stamp-gap report) look up an addrs.ConfigResource. Resolution walks KEYED
// module instances, so taking the address off a resolution without dropping
// the key rendered module.wrapped["a"].aws_eip.app while the readers looked
// up module.wrapped.aws_eip.app; inside a for_each'd module the two could
// never match, which silently downgraded the must-stamp error to a warning.
// See GitHub issue #111. This is the single implementation of that keying,
// which internal/live/check and internal/command previously kept two copies
// of.
//
// One block's instances can disagree about the cause - `name = each.value.n`
// resolves for one for_each key and is null for another - and a block whose
// instances disagree maps to [DiscoveryCauseUnspecified] rather than to
// whichever instance was walked first, so the sentence a reader renders from
// it is true of every instance or says nothing specific at all. The fold is
// order-independent: disagreement is absorbing.
func (res *Result) DiscoveryCausesByBlock() map[string]BlockDiscovery {
	if res == nil {
		return nil
	}
	needs := res.NeedsDiscovery()
	out := make(map[string]BlockDiscovery, len(needs))
	for _, r := range needs {
		key := r.Addr.ContainingResource().Config().String()
		got := BlockDiscovery{Cause: r.Cause.Normalize(), Args: r.CauseArgs}
		if prev, seen := out[key]; seen && !prev.sameAs(got) {
			got = BlockDiscovery{Cause: DiscoveryCauseUnspecified}
		}
		out[key] = got
	}
	return out
}
