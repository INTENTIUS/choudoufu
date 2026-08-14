// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Ownership is the rule that decides which live objects a projection is
// allowed to contain.
//
// Without it, a projection materializes whatever the provider returns for an
// identity that came out of configuration - which is exactly what a
// client-named resource's identity is. A configuration naming a bucket, a log
// group or a role that already exists and belongs to somebody else therefore
// adopted it: the live object entered the prior state, the plan proposed
// in-place updates to it, and deleting the block later proposed destroying it.
// The estate had never owned it and nothing in the run ever said so, because
// the foreign classifier only ever sees resources that reached it through
// discovery, and a client-named resource never does (audit finding C1).
//
// The rule this type applies is the marker spec's, applied to the object the
// provider just handed over rather than to the configuration that named it:
// a live resource that can carry an ownership marker enters the prior state
// only if it carries this estate's. Nothing else is trusted, and specifically
// not the configuration's own tags argument, because in the case that matters
// the configuration is the thing making the unfounded claim.
type Ownership struct {
	// Estate is the tofu-estate marker value a live object must carry. An
	// empty name means this run established no estate at all: nothing can be
	// verified, so nothing marker-capable is admitted, and the omissions say
	// exactly that rather than a run quietly adopting whatever it found.
	Estate string

	// Verified names the instance addresses whose ownership the caller has
	// already established, keyed by address string, which are admitted without
	// a second look. It is the discovery package's MarkerVerified set: an instance
	// bound by marker discovery was found *by* its marker, so re-deriving the
	// same fact from the same tags would only be a way to get it wrong twice.
	Verified map[string]bool

	// Policy is GitHub issue #67's resolved ownership policy. Nil is today's
	// fixed behavior: a declared+tagged instance converges and a declared+
	// untagged one is refused, exactly as [checkOwnership] read them before
	// Policy existed. Set, it governs both declared quadrants - see
	// [builder.checkPolicy].
	Policy *policy.Policy
}

// verified reports whether the caller already established ownership of an
// instance.
func (o *Ownership) verified(addr addrs.AbsResourceInstance) bool {
	return o != nil && o.Verified[addr.String()]
}

// Unowned is a live object the projection refused to admit: something exists
// at the identity a declared resource names, and it does not carry this
// estate's ownership marker.
//
// It is the client-named counterpart of the foreign section a marker-path
// resource gets, and it means the same thing: the resource is not this
// estate's, nothing in the plan will touch it, and adopting it is a tag write
// a human performs deliberately.
type Unowned struct {
	// Addr is the declared instance whose identity found it.
	Addr addrs.AbsResourceInstance

	// TypeName is the resource type.
	TypeName string

	// ImportID is the identity the live object was read with, which is also
	// the identity a human needs in order to go look at it.
	ImportID string

	// Estate is the tofu-estate marker the live object carries, empty when it
	// carries none. Another estate's name here means the resource is owned,
	// just not by this run.
	Estate string

	// Detail is one sentence aimed at an operator.
	Detail string
}

// String renders one unowned resource on a line, for logs and test failures.
func (u Unowned) String() string {
	return u.Addr.String() + " UNOWNED " + u.TypeName + "/" + u.ImportID
}

// ownershipVerdict is what the check decided about one live object.
type ownershipVerdict int

const (
	// ownershipOK admits the object into the prior state.
	ownershipOK ownershipVerdict = iota
	// ownershipUnowned keeps it out: it carries no marker, or another
	// estate's.
	ownershipUnowned
)

// checkOwnership applies [Ownership] to one materialized object, and - when
// [Ownership.Policy] is set - GitHub issue #67's policy verb for whichever
// of the two declared quadrants this object falls in.
//
// "Tagged", for this function's purposes, always means "already carries
// this estate's tofu-estate marker", never the policy's own configurable
// TagKey/TagValue. That is a deliberate narrowing: TagKey defaults to the
// estate marker, so under the common configuration the two readings
// coincide exactly and this function's behavior is unaffected either way.
// Where they diverge - a policy block naming a distinct preservation tag -
// admission stays keyed on estate ownership, the question this function
// exists to answer, and the preservation tag's own effect is confined to
// the untag verb (which tag it releases, in internal/live/stamp) and to the
// undeclared quadrants (internal/live/discovery), where "tagged" already
// has to mean the configured tag because there is no estate ownership to
// fall back on for a resource nothing declares. Folding a stray
// preservation tag into whether an otherwise-unrelated declared resource is
// admitted at all would make a policy block written for account
// reconciliation silently start rejecting resources this estate already
// manages, which is not power the issue asks this quadrant to have.
//
// A type with nowhere to put a marker is admitted: the marker contract is
// defined over taggable types, and the resources in the v0 subset that carry
// no tags - a bucket policy, a role policy attachment, a route, a route table
// association - are identified by a composite of their parents, so their
// ownership is their parents' ownership and a rule pretending otherwise would
// only ever produce a false accusation. That boundary is stated in
// live/MARKERS.md and is the one gap in "nothing enters the prior state
// unverified" that is a property of the cloud rather than of this code.
// declared says whether addr has a resource block in this configuration -
// false for a sweep orphan, a parent-read finding, or a scoped
// reconciliation candidate, every one of which reaches this function on the
// same materialize path a declared instance does, but is never
// declared_tagged: that quadrant's policy is discovery's job
// (internal/live/discovery's applyOrphanPolicy) or, for a reconciliation
// candidate, already decided by the threshold guard before it ever got
// here. Passing verified=true (own.verified(addr)) without also checking
// declared would read an orphan as declared_tagged and hand it a
// declared-quadrant verb it was never assigned - see
// TestOwnershipPolicy_ReconcileCandidateIsNotDeclaredTagged.
func (b *builder) checkOwnership(addr addrs.AbsResourceInstance, typeName, importID string, schema providers.Schema, obj cty.Value, declared bool) ownershipVerdict {
	own := b.opts.Ownership
	switch {
	case own == nil:
		return ownershipOK
	case own.verified(addr):
		// Marker discovery already proved ownership by finding this
		// instance's marker - which is only possible when its live tags
		// carry this estate's tofu-estate. When the instance is also
		// declared, that makes it declared_tagged by construction, and
		// this is the one place policy can see it: a needs-discovery
		// instance (the common case for declared_tagged - a VPC, a
		// subnet) never reaches the tag read below at all, so recording
		// its policy outcome has to happen here, on the same
		// "tagged=true" reading the tag-reading path below would have
		// computed for it. An undeclared but verified instance - an
		// orphan, a parent-read finding, a reconciliation candidate - is
		// never declared_tagged and this function has nothing to say
		// about it.
		if declared {
			if verb := own.Policy.Verb(true, true); verb != policy.DefaultVerb[policy.DeclaredTagged] {
				b.policyList = append(b.policyList, PolicyOutcome{Addr: addr, TypeName: typeName, Tagged: true, Verb: verb})
			}
		}
		return ownershipOK
	case !markerCapable(schema.Block):
		return ownershipOK
	}

	tags, taggable := markers.TagsOf(obj)
	if !taggable {
		// The schema says the type is taggable and the object came back
		// without the attribute. That is a provider bug rather than an
		// ownership fact, and it is not a licence to adopt: the object has
		// nothing on it that says whose it is.
		b.unowned(addr, typeName, importID, "", fmt.Sprintf(
			"The provider read the %s with identity %q back without a tags attribute, so nothing on it says which estate owns it. A resource enters the prior state only when it carries this estate's %s marker, so it was left alone: nothing in this plan reads, changes or destroys it.",
			typeName, importID, markers.TagEstate), false)
		return ownershipUnowned
	}

	estate := tags[markers.TagEstate]
	tagged := estate != "" && own.Estate != "" && estate == own.Estate

	verb := own.Policy.Verb(true, tagged)
	nonDefault := verb != policy.DefaultVerb[quadrantFor(tagged)]
	if nonDefault {
		switch verb {
		case policy.Converge, policy.Adopt, policy.Untag:
			// Admit regardless of tagged state: declared_tagged's converge
			// and untag both keep managing the resource (untag only
			// changes what stamping does to its tags - see
			// internal/live/stamp), and declared_untagged's adopt/converge
			// is the auto-admit this quadrant did not have before. A
			// declared address names the resource explicitly, so there is
			// no guess the way a content-matched bind candidate
			// (internal/live/foreign) would be.
			b.policyList = append(b.policyList, PolicyOutcome{Addr: addr, TypeName: typeName, Tagged: tagged, Verb: verb})
			return ownershipOK
		case policy.Keep, policy.Report:
			b.policyList = append(b.policyList, PolicyOutcome{Addr: addr, TypeName: typeName, Tagged: tagged, Verb: verb})
			if tagged {
				// declared_tagged keep/report: still admitted, so ordinary
				// convergence continues on every attribute but the policy
				// tag - see internal/live/stamp for the tag-specific
				// exemption these two verbs give a resource.
				return ownershipOK
			}
			// declared_untagged keep/report: quieter variants of refuse -
			// the same non-admission, softer (keep) or explicitly labeled
			// (report) messaging. Falls through to the rejection below.
		}
	}

	if tagged {
		return ownershipOK
	}

	var detail string
	switch {
	case own.Estate == "":
		detail = fmt.Sprintf(
			"A live %s exists with identity %q, and this run has no estate name, so there is nothing to check its ownership marker against. Pass -estate=<name>, or name the estate in the live block, and re-run. See live/MARKERS.md, \"Ownership semantics\".",
			typeName, importID)
	case estate == "":
		detail = fmt.Sprintf(
			"A live %s already exists with identity %q and carries no %s marker, so this estate does not own it and the plan proposes creating the resource this configuration declares - which, for a type whose name must be unique, the cloud will refuse while the unowned one holds it. Adopt it by writing %s=%q and %s=%q onto it, then re-run; or set policy { declared_untagged = \"adopt\" } in the live block to have this run adopt it for you; or point this resource at a name nobody is using.",
			typeName, importID, markers.TagEstate,
			markers.TagEstate, own.Estate,
			markers.TagAddress, markers.EscapeAddress(addr.String()))
	default:
		detail = fmt.Sprintf(
			"A live %s already exists with identity %q and carries %s=%q, so it belongs to another estate and nothing in this plan reads, changes or destroys it. See live/MARKERS.md, \"Ownership semantics\".",
			typeName, importID, markers.TagEstate, estate)
	}
	b.unowned(addr, typeName, importID, estate, detail, nonDefault && verb == policy.Keep)
	return ownershipUnowned
}

// quadrantFor is the declared-side quadrant one "tagged" reading names.
func quadrantFor(tagged bool) policy.Quadrant {
	if tagged {
		return policy.DeclaredTagged
	}
	return policy.DeclaredUntagged
}

// unowned records one refusal: on the result, in the omissions, and - unless
// quiet - as a warning an operator reading a plan cannot miss.
//
// A warning rather than an error on purpose. "Something else already holds
// this name" is a fact about the world, not a broken run, and the plan that
// follows is correct and safe: it proposes creating what the configuration
// declares and it proposes nothing at all about the resource nobody here
// owns. Failing the run would also make the one legitimate first step -
// planning to see what is in the way - impossible.
//
// quiet is set only for declared_untagged's "keep" verb: the refusal itself
// is unchanged (nothing is admitted, nothing is planned), but "keep" is
// documented as a quieter variant of refuse, so the loud warning a plain
// refusal always carries is left off. The fact still travels in
// [Result.Unowned] and [Result.Omitted] for any caller that wants it.
func (b *builder) unowned(addr addrs.AbsResourceInstance, typeName, importID, estate, detail string, quiet bool) {
	b.unownedList = append(b.unownedList, Unowned{
		Addr:     addr,
		TypeName: typeName,
		ImportID: importID,
		Estate:   estate,
		Detail:   detail,
	})
	if !quiet {
		b.diags = b.diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Live resource outside this estate",
			detail,
		))
	}
	b.omit(addr, ReasonUnowned, detail, fmt.Sprintf(
		"the live %s at its identity carries no ownership marker for this estate.", typeName))
}

// markerCapable reports whether a resource type has anywhere to carry an
// ownership marker, read from the provider's own schema for the type. It is
// the same question [discovery.markerCapable] asks of a list schema, asked of
// the managed resource schema a projection has in hand.
func markerCapable(block *configschema.Block) bool {
	if block == nil {
		return false
	}
	for _, name := range []string{"tags", "tags_all"} {
		if _, ok := block.Attributes[name]; ok {
			return true
		}
	}
	return false
}
