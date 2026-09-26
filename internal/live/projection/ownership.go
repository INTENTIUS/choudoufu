// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/moved"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/substrate"
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

	// AddressMarker reports whether this resource type's marker surface
	// carries a tofu-address beside the estate marker - true for the AWS
	// tag map, false for both Kubernetes label surfaces, where #1016 ruled
	// the marker is the estate label alone because the object's own group,
	// kind, namespace and name are the join key back to configuration.
	//
	// It exists so that whoever renders the adoption hint offers the write
	// that would actually adopt this object. Before GitHub issue #1108 no
	// Kubernetes object could ever become an Unowned entry, so the hint
	// naming two tags was right everywhere it could be printed; the moment
	// the label is read it is printed on objects where writing a
	// tofu-address label is not the adoption and would leave a marker
	// nothing reads.
	AddressMarker bool

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
	// ownershipStale is GitHub issue #364 unit B's third answer, reachable
	// only when recordFirst is set: the object [builder.materializeFromRecord]
	// found through the estate's record store does not carry a tofu-address
	// marker naming this instance (or, for a taggable schema, carries no
	// tags at all), so the record cannot be trusted as this binding's proof
	// the way [ownershipOK]'s located branch trusts one. It is not
	// [ownershipUnowned]: nothing here says the object belongs to someone
	// else, only that this record no longer proves it belongs to this
	// instance, and the caller falls back to whatever identity.Class would
	// have done with no record in play - the marker sweep or static
	// derivation - rather than being refused outright.
	ownershipStale
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
// "Marker", likewise, is whichever carrier the type's own schema has -
// [markerSurfaceOf]. GitHub issue #1108: the surface read used to be the
// AWS tag map and nothing else, so every Kubernetes type fell through the
// "nowhere to put a marker" case below and was admitted without its label
// ever being read. The consequences were both of the ones this function
// exists to prevent, on a whole substrate: an object carrying another
// estate's label was bound by its natural key and the plan proposed
// relabelling it to this estate, leaving the admission policy of #1066 -
// where a cluster admin has installed it - as the only thing between that
// plan and a wrong marker written over somebody else's; and an unlabelled
// object was adopted with nothing said. Reading the label puts both back
// where the tag surface already had them: the first refuses by name with
// the sentence below that names the estate the object does carry, the
// second is declared_untagged and its policy verb decides.
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
// TestOwnershipPolicy_ReconcileCandidateIsNotDeclaredTagged. The same holds
// for an undeclared instance that is NOT verified and so reaches the tag
// read: it is never handed a declared-quadrant verb either (GitHub issue
// #1226, and the comment at the verb lookup below).
//
// recordFirst is GitHub issue #364 unit B's addition: true when the
// identity being checked came from [builder.materializeFromRecord]'s
// universal, ahead-of-class record read rather than from identity.Class's
// own ClassRecordLocated route. It changes nothing when located is also
// true (an operator's `markers = record` selection still trusts
// unconditionally - see the located case below) or when the type has
// nowhere to carry a marker (nothing to check the record against either
// way). Where it changes the answer is exactly the case ClassRecordLocated
// never reached before this issue: a TAGGABLE type bound by a record. There
// the record is evidence, not proof, and this function verifies it against
// the live object's own tofu-address before trusting it - see
// [ownershipStale].
func (b *builder) checkOwnership(addr addrs.AbsResourceInstance, typeName, importID string, schema providers.Schema, obj cty.Value, declared, located, recordFirst bool) ownershipVerdict {
	own := b.opts.Ownership
	surface := markerSurfaceOf(schema.Block)
	switch {
	case own == nil:
		return ownershipOK
	case located:
		// The identity came out of this estate's own located record store,
		// which is the ownership proof for this instance: the record was
		// written by this estate's apply, under its own IAM, keyed by this
		// exact address, and [LocatedStore.Get] has already refused a
		// payload naming any other one. Re-deriving ownership from a tag
		// would only be a way to get the same fact wrong twice - the same
		// reason own.verified short-circuits marker discovery's answer
		// below.
		//
		// Before GitHub issue #365 this branch was unreachable and unneeded:
		// every located type was markerless, so markerCapable(schema.Block)
		// was false and the case below admitted it. An operator's
		// `markers = record` selection is the first way a TAGGABLE instance
		// reaches here with a record-held identity, and without this branch
		// it read as declared_untagged - the adoption-refusal quadrant -
		// which would have kept the object out of the prior state and had
		// every plan propose creating a duplicate of it. Nothing about that
		// failure is visible in a verdict: the plans stay clean and the
		// account grows a second object per run.
		//
		// It is deliberately not folded into the own.verified case above,
		// which also records a declared_tagged policy outcome. This instance
		// is not tagged, and reporting it as though it were would tell a
		// policy block's declared_tagged verb it had governed something it
		// never saw.
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
	case b.envelopeAdmitted[addr.String()]:
		// The cache-hit rule's second arm vouched this instance (issue
		// #692 increment 2, maintainer ruling 2026-09-01 on that issue):
		// the listing pass saw its identity live and the record envelope
		// attests this estate owns it. The object being inspected here is
		// the cache-served one, whose tags are whatever the cache
		// remembered - reading ownership off it would trust exactly the
		// file the stale-state ruling forbids trusting. The record is the
		// ownership proof, on the same reasoning as the located branch
		// above; the known bound (an out-of-band marker strip stays
		// invisible until the next full read) is priced by the
		// -refresh=false contract this arm only ever runs under.
		return ownershipOK
	case surface == surfaceNone:
		return ownershipOK
	}

	tags, taggable := surface.markersOf(obj)
	if !taggable {
		if recordFirst {
			// Nothing on the object says whose it is, which is exactly
			// what a stale record looks like when there is no tags
			// attribute at all to disagree with by value. Fall back rather
			// than refuse: identity.Class's own recovery path gets a
			// chance to find this instance some other way.
			b.recordStale(addr, typeName, importID, "")
			return ownershipStale
		}
		// The schema says the type is taggable and the object came back
		// without the attribute. That is a provider bug rather than an
		// ownership fact, and it is not a licence to adopt: the object has
		// nothing on it that says whose it is.
		b.unowned(addr, typeName, importID, "", fmt.Sprintf(
			"The provider read the %s with identity %q back without a %s, so nothing on it says which estate owns it. A resource enters the prior state only when it carries this estate's %s marker, so it was left alone: nothing in this plan reads, changes or destroys it.",
			typeName, importID, surface.carrierPhrase(), markers.TagEstate), noMarkerCause(typeName), surface.carriesAddress(), false)
		return ownershipUnowned
	}

	if recordFirst && surface.carriesAddress() {
		// The stale-record rule ("In upstream terms", #389, ruled
		// 2026-08-23): a record is trusted for a taggable type only while
		// the live object's own tofu-address marker still names this
		// instance. Checked with the identical rule [addressNames] applies
		// to a config-derived identity's second look ([moved.Accepts],
		// so a `moved` block or an older escaping grammar still counts as
		// naming this instance) - but unlike addressNames, an ABSENT
		// marker is not the safe "nothing to stamp yet" case here: that
		// reading is sound when the identity came from the configuration
		// itself, which is independent evidence the object is this
		// instance's. A record is not independent evidence of anything
		// once the marker it should have written stops confirming it, so
		// silence is treated the same as disagreement.
		raw, corrupt := markers.GatherAddress(tags)
		if corrupt || raw == "" || !moved.Accepts(b.movedStmts, addr, markers.EscapeAddress(raw)) {
			b.recordStale(addr, typeName, importID, raw)
			return ownershipStale
		}
	}

	estate := tags[markers.TagEstate]
	tagged := estate != "" && own.Estate != "" && estate == own.Estate

	if estate != "" && own.Estate != "" && !tagged {
		// GitHub issue #1166, ruled 2026-09-16. An object carrying another
		// estate's tofu-estate is not untagged: it is tagged, for somebody
		// else. Reading "untagged" as "does not carry THIS estate's
		// marker" put it in the declared_untagged quadrant, where
		// policy { declared_untagged = "adopt" } admitted it and the stamp
		// then wrote this estate's marker over the other estate's - the
		// wrong-marker class, reached silently from a policy block written
		// about unmarked resources.
		//
		// So it sits outside the quadrants entirely, exactly as
		// [builder.addressNames] does and for the same reason: the
		// quadrant question does not apply to it. Not just the admitting
		// verbs - the verb is never consulted, no declared-quadrant
		// outcome is recorded, and the refusal is never quiet, because
		// "keep" and "report" are declared_untagged affordances and this
		// object is not in that quadrant.
		//
		// Taking such an object over remains possible; it is the silence
		// that is removed. `live-import -approve` and `live-mv
		// -from-estate` are the deliberate routes, both unchanged by this,
		// and the refusal names them.
		b.unowned(addr, typeName, importID, estate, anotherEstateDetail(typeName, importID, estate, own.Policy.Verb(true, false)),
			noMarkerCause(typeName), surface.carriesAddress(), false)
		return ownershipUnowned
	}

	if tagged && surface.carriesAddress() {
		// This estate's marker is on the object, so the second half of the
		// marker spec's ownership question applies: WHICH of this estate's
		// instances is it? GitHub issue #244 - both this layer and discovery
		// deferred that to the other, in comments, and neither performed it.
		//
		// Only on the tag surface: see [markerSurface.carriesAddress].
		if detail, cause, ok := b.addressNames(addr, typeName, importID, tags); !ok {
			b.unownedAddress(addr, typeName, importID, estate, detail, cause)
			return ownershipUnowned
		}
	}

	// GitHub issue #1226. The verb is a DECLARED quadrant's verb, so it is
	// asked for only when the instance is declared. The first argument here
	// was the literal true, which read every instance that got this far as
	// declared whatever the caller had said, and handed an undeclared one
	// declared_untagged's "adopt" or declared_tagged's "untag" - the hazard
	// the own.verified case above already guards, on the path that case does
	// not cover.
	//
	// An undeclared instance gets no verb from this function at all, rather
	// than Verb(false, tagged). Neither undeclared quadrant has a verb that
	// means anything here ([policy.ValidVerbs]): none admits, and the two
	// that act - undeclared_tagged's delete and untag, undeclared_untagged's
	// scoped delete - are internal/live/discovery's, decided before the
	// resolution ever reached the projection (applyOrphanPolicy, and the
	// reconcile pass's threshold guard, whose candidates arrive vouched
	// through [Ownership.Verified]). So the verdict for an undeclared
	// instance is the marker's alone: carrying this estate's marker and its
	// own address it is admitted, which is what makes it destroyable, and
	// carrying none it is foreign - live/MARKERS.md, "Ownership semantics":
	// "reported, protected, and never auto-deleted". Admitting that one is
	// the opposite of protecting it, because an instance with no resource
	// block can only ever be planned as a destroy. No [PolicyOutcome] is
	// recorded either way: that type is a declared instance's by definition.
	var verb policy.Verb
	nonDefault := false
	if declared {
		verb = own.Policy.Verb(declared, tagged)
		nonDefault = verb != policy.DefaultVerb[policy.QuadrantOf(declared, tagged)]
	}
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
	case !declared:
		// Foreign, not declared_untagged (#1226). The two wordings below
		// both speak of "the resource this configuration declares" and
		// offer policy { declared_untagged = "adopt" }; neither is true of
		// an instance with no resource block, and the second would send an
		// operator to a verb that does not reach this object.
		detail = fmt.Sprintf(
			"A live %s with identity %q carries no %s marker and nothing in this configuration declares %s, so it was left out of the prior state and this plan does not touch it. policy { declared_untagged } does not apply to a resource the configuration does not declare. To manage it, add a resource block for it and re-run.",
			typeName, importID, markers.TagEstate, addr)
	case estate == "" && !surface.carriesAddress():
		// The Kubernetes wording. Same quadrant, same verdict, same two
		// ways out; what differs is that the marker to write is one
		// label and there is no address to write beside it (#1016).
		detail = fmt.Sprintf(
			"A live %s already exists with identity %q and carries no %s label, so this estate does not own it and the plan proposes creating the resource this configuration declares - which, for an object the API server keys by namespace and name, the cluster will refuse while the unowned one holds it. Adopt it by writing the label %s=%q onto it, then re-run; or set policy { declared_untagged = \"adopt\" } in the live block to have this run adopt it for you; or point this resource at a name nobody is using.",
			typeName, importID, markers.TagEstate,
			markers.TagEstate, own.Estate)
	default:
		// estate == "" by construction: the other-estate reading returned
		// above (#1166) and the tagged reading is admitted, so the only
		// case left with own.Estate set is an object carrying no estate
		// marker at all.
		detail = fmt.Sprintf(
			"A live %s already exists with identity %q and carries no %s marker, so this estate does not own it and the plan proposes creating the resource this configuration declares - which, for a type whose name must be unique, the cloud will refuse while the unowned one holds it. Adopt it by writing %s=%q and %s=%q onto it, then re-run; or set policy { declared_untagged = \"adopt\" } in the live block to have this run adopt it for you; or point this resource at a name nobody is using.",
			typeName, importID, markers.TagEstate,
			markers.TagEstate, own.Estate,
			markers.TagAddress, markers.EscapeAddress(addr.String()))
	}
	b.unowned(addr, typeName, importID, estate, detail, noMarkerCause(typeName), surface.carriesAddress(), nonDefault && verb == policy.Keep)
	return ownershipUnowned
}

// The two summaries this file's refusals carry, and this file's two entries
// in [refusals]. They are separated because they mean different things to an
// operator: the first is "somebody else's resource is in the way", a fact
// about the account; the second is "this estate's own marker disagrees with
// this estate's own configuration", which is the wrong-marker class and
// always needs a human.
const (
	SummaryOutsideEstate = "Live resource outside this estate"
	SummaryWrongAddress  = "Live resource marked for another address"
)

// anotherEstateDetail is the refusal an object carrying a DIFFERENT
// estate's tofu-estate gets (GitHub issue #1166).
//
// It says three things, and the second and third are the issue's point. The
// operator asked for something in writing and is being refused, so the
// message has to say why the verb did not reach this object rather than
// leaving them to infer that "untagged" excluded "tagged for someone else";
// and a refusal that only says no teaches nothing, so it names the two
// sanctioned routes for taking an object over deliberately. Neither route
// is affected by this narrowing - `live-import -approve` and `live-mv
// -from-estate` still cross the estate boundary on purpose, which is what
// makes refusing the silent crossing reasonable.
//
// verb is the verb declared_untagged resolves to for this run. It is
// reported only when it is something other than the default refusal,
// because that is the only case where the operator wrote an instruction
// this object did not obey.
func anotherEstateDetail(typeName, importID, estate string, verb policy.Verb) string {
	var asked string
	if verb != policy.DefaultVerb[policy.DeclaredUntagged] {
		asked = fmt.Sprintf(
			" policy { %s = %q } did not govern it: that quadrant is about objects carrying no estate marker at all, and this one is marked - for somebody else.",
			policy.DeclaredUntagged.Attribute(), verb)
	}
	return fmt.Sprintf(
		"A live %s already exists with identity %q and carries %s=%q, so it belongs to another estate and nothing in this plan reads, changes or destroys it.%s Taking it over is something you do deliberately: `live-mv -from-estate=%s` rewrites the marker to this estate one address at a time, and `live-import -approve` stamps this estate's markers over the resources a state file lists. Or point this resource at a name nobody is using. See live/MARKERS.md, \"Ownership semantics\".",
		typeName, importID, markers.TagEstate, estate, asked, estate)
}

// noMarkerCause is the subordinate clause a dependent instance's own omission
// nests, for the estate-marker half of the check.
func noMarkerCause(typeName string) string {
	return fmt.Sprintf("the live %s at its identity carries no ownership marker for this estate.", typeName)
}

// addressNames answers the second half of the ownership question, for a live
// object already established to carry this estate's tofu-estate marker: does
// its tofu-address marker name THIS instance?
//
// It returns the operator-facing detail and the nesting clause for a
// refusal, and false, when it does not. Until GitHub issue #244 this function
// did not exist and every answer was "yes, adopt it".
//
// # The one shape that is NOT refused, and why
//
// A live object carrying this estate's tofu-estate marker and NO tofu-address
// marker at all is admitted, and the plan's ordinary tags diff then writes
// the missing marker. That is a deliberate decision, not an oversight, and it
// is at first reading in tension with live/MARKERS.md, "Ownership semantics",
// which calls a resource with tofu-estate and a missing or unparseable
// tofu-address malformed and says it is "never guessed at". The reconciliation
// is what the two layers each have in hand:
//
//   - internal/live/discovery meets such an object during a scan or a sweep,
//     where the marker is the ONLY thing that could attach it to an instance.
//     With no address there is nothing to attach it by, so guessing is the
//     only remaining move and it correctly refuses to - ProblemMalformedMarker.
//   - This function meets it at an identity the CONFIGURATION named. The
//     object was fetched by the import ID a declared instance computed, so
//     "which instance is this" is already answered by evidence that is not the
//     marker, and admitting it is not a guess. What is missing is a marker
//     this run is about to write.
//
// That is exactly the shipped migration path for a resource stamped by a run
// older than the tofu-address marker, and it is what
// TestLivePlan_stampsMissingMarkers in internal/command asserts, down to the
// `+ "tofu-address"` line in the plan. Refusing it would break every estate
// carrying such a resource, in exchange for nothing: an absent marker
// contradicts no other claim, so nothing can be leaked or double-claimed by
// completing it.
//
// A tofu-address that is present and names another instance is the opposite
// case in every respect. There the estate's own marker and the estate's own
// configuration disagree about one object, adopting it makes this plan rewrite
// a marker off an object that belongs to a sibling instance, and the object
// that sibling's address named is left behind still carrying it - two live
// objects, one estate, one address, which live/MARKERS.md names as an error.
// That one is refused.
//
// A gapped continuation chain is refused with it, not adopted with the absent
// case: a chain with a hole in it IS a competing claim, just an unreadable
// one, and the address it would have spelled could perfectly well be another
// instance's. Adopting it would destroy the only evidence of that.
//
// The comparison goes through [moved.Accepts], which is the single definition
// of "this marker names this instance" that discovery's own index is also
// built from ([moved.Aliases]). Two things follow from that and both are
// load-bearing:
//
//   - A `moved` block legitimately leaves the live object carrying the OLD
//     address until the marker rewrite lands, and that rewrite is the ordinary
//     tags diff the provider plans (GitHub issue #198). A bare equality here
//     would refuse every configuration with a moved block in it.
//   - A marker written under an older escaping grammar still names the
//     instance it always named, because [markers.AddressMatches] tries all
//     three grammars this fork has stamped with. A bare string comparison
//     against [markers.EscapeAddress] reintroduces the cross-grammar hole that
//     produced 107 false positives earlier in this campaign.
//
// This runs BEFORE the ownership policy's verbs, and deliberately outside
// them. A wrong address is not the declared_untagged quadrant, however much
// "not tagged for me" sounds like it: the object IS tagged, by this estate,
// for another one of its own instances. Folding it into that quadrant would
// let policy { declared_untagged = "adopt" } adopt a sibling instance's live
// object, and stamping would then write this instance's address onto it -
// producing the two-objects-one-address collision live/MARKERS.md names as an
// error, manufactured by the tool.
//
// live-mv is unaffected: its plan path materializes through
// [BuildFrom], which passes a zero Options, so Ownership is nil and this
// function is never reached. That is checked by
// TestOwnershipAddress_mvMaterializePathIsUnaffected.
func (b *builder) addressNames(addr addrs.AbsResourceInstance, typeName, importID string, tags map[string]string) (detail, cause string, ok bool) {
	raw, corrupt := markers.GatherAddress(tags)
	switch {
	case corrupt:
		return fmt.Sprintf(
			"A live %s with identity %q carries this estate's %s marker, but its %s continuation tags have a gap in them - one of %s-2, %s-3, ... is missing while a later one is present. Per live/MARKERS.md such a resource is malformed: neither owned nor foreign, and never read as the address up to the gap. It was left out of this plan, which therefore proposes creating %s. Repair the tags by hand and re-run.",
			typeName, importID, markers.TagEstate, markers.TagAddress,
			markers.TagAddress, markers.TagAddress, addr,
		), fmt.Sprintf("the live %s at its identity carries a malformed %s marker.", typeName, markers.TagAddress), false

	case raw == "":
		// No competing claim to contradict the identity the configuration
		// computed. Admitted, and this run stamps the marker it is missing -
		// see this function's doc comment for why this is the one shape not
		// refused, and why that does not contradict live/MARKERS.md.
		return "", "", true

	case !moved.Accepts(b.movedStmts, addr, markers.EscapeAddress(raw)):
		return fmt.Sprintf(
			"A live %s with identity %q carries this estate's %s marker and %s=%q, which names a different resource than %s. The identity this configuration computes for %s therefore lands on an object another instance of this same estate owns. Adopting it would make this plan rewrite that object's %s marker onto %s and leave the object that address named behind, still marked, which live/MARKERS.md names as an error. It was left out of this plan instead. If the resource really did move, say so with a moved block or with choudoufu live-mv; if two instances have renumbered onto each other, that is what has to be fixed.",
			typeName, importID, markers.TagEstate, markers.TagAddress, raw, addr,
			addr, markers.TagAddress, addr,
		), fmt.Sprintf("the live %s at its identity carries another address's %s marker.", typeName, markers.TagAddress), false
	}
	return "", "", true
}

// SummaryStaleRecord is the summary [builder.recordStale]'s diagnostic
// carries - registered in refusals.go so internal/live/refusalscan's lockstep
// test can find it, the way [SummaryOutsideEstate] and [SummaryWrongAddress]
// are.
const SummaryStaleRecord = "Record does not match the live marker"

// recordStale reports GitHub issue #364 unit B's stale-record finding: a
// record [builder.materializeFromRecord] read named a live object at
// importID, but the object's own tofu-address marker does not confirm the
// binding - either it names some other address, or (foundAddress == "") it
// carries no address marker to check at all. Always a warning, never an
// error: the plan that follows is correct and safe either way, because the
// instance falls back to its identity.Class's own recovery path rather than
// being left out of the plan the way [builder.unowned]'s refusal leaves an
// instance out.
//
// No [Omission] or [Unowned] entry is recorded here, unlike every other
// finding in this file: this is not this function's last word on addr. The
// caller ([builder.applyRecordFirst], through [builder.materialize]'s
// ownershipStale case) returns the resolution to normal routing, and
// whatever that produces - materialized, absent, or a fresh refusal of its
// own - is the answer that belongs in the result, not this one.
func (b *builder) recordStale(addr addrs.AbsResourceInstance, typeName, importID, foundAddress string) {
	var found string
	if foundAddress == "" {
		found = "carries no address marker at all"
	} else {
		found = fmt.Sprintf("names %q instead", foundAddress)
	}
	detail := fmt.Sprintf(
		"The estate's record for %s pointed at a live %s with identity %q, but that object's own %s marker %s. Treating the record as stale: this instance falls back to marker discovery or static derivation, exactly as if no record existed for it.",
		addr, typeName, importID, markers.TagAddress, found,
	)
	log.Printf("[WARN] projection: %s", detail)
	b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryStaleRecord, detail))
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
//
// summary and cause are parameters rather than constants because this
// function now serves two different refusals: "this is somebody else's
// resource" and GitHub issue #244's "this is this estate's own resource,
// marked for a different one of its instances". Both keep a resource out of
// the prior state on the same terms, and they must not read as the same
// finding - each has its own entry in [refusals].
func (b *builder) unowned(addr addrs.AbsResourceInstance, typeName, importID, estate, detail, cause string, addressMarker, quiet bool) {
	b.unownedList = append(b.unownedList, Unowned{
		Addr:          addr,
		TypeName:      typeName,
		ImportID:      importID,
		Estate:        estate,
		AddressMarker: addressMarker,
		Detail:        detail,
	})
	if !quiet {
		b.diags = b.diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			SummaryOutsideEstate,
			detail,
		))
	}
	b.omit(addr, ReasonUnowned, detail, cause)
}

// unownedAddress is [builder.unowned] for GitHub issue #244's refusal: this
// estate's own marker, naming a different one of its own instances (or no
// instance at all). It records exactly the same three things - the Unowned
// entry, the warning, the omission - under its own summary, because the two
// findings mean different things to whoever reads the plan and because a
// refusal that cannot be told apart from another cannot be looked up.
//
// It is never quiet. The quiet variant exists only for declared_untagged's
// "keep" verb, and a wrong address is not that quadrant - see
// [builder.addressNames].
func (b *builder) unownedAddress(addr addrs.AbsResourceInstance, typeName, importID, estate, detail, cause string) {
	b.unownedList = append(b.unownedList, Unowned{
		Addr:     addr,
		TypeName: typeName,
		ImportID: importID,
		Estate:   estate,
		// Only the tag surface reaches this refusal at all - see
		// [markerSurface.carriesAddress].
		AddressMarker: true,
		Detail:        detail,
	})
	b.diags = b.diags.Append(tfdiags.Sourceless(
		tfdiags.Warning,
		SummaryWrongAddress,
		detail,
	))
	b.omit(addr, ReasonUnowned, detail, cause)
}

// markerSurface names which carrier a resource type has for its ownership
// marker. GitHub issue #1108: until it there was only one, and the switch
// below returned "no surface" for every Kubernetes type, so [checkOwnership]
// admitted a Kubernetes object without ever reading its label - another
// estate's object relabelled by the plan, an unlabelled one adopted in
// silence, and the ownership policy's verbs never reached on that substrate
// at all.
type markerSurface int

const (
	// surfaceNone is a type with nowhere to carry a marker. See
	// [checkOwnership]'s doc comment for why that is admitted rather than
	// refused.
	surfaceNone markerSurface = iota
	// surfaceTags is the AWS shape: a settable top-level tags map holding
	// tofu-estate and tofu-address.
	surfaceTags
	// surfaceLabels is the Kubernetes metadata-block shape
	// ([markers.LabelSurface]): metadata[0].labels, holding tofu-estate
	// alone.
	surfaceLabels
	// surfaceManifest is the kubernetes_manifest shape
	// ([markers.ManifestSurface]): manifest.metadata.labels, again
	// tofu-estate alone.
	surfaceManifest
)

// markerSurfaceOf reads a type's marker carrier off the provider's own
// schema for it, never off a list of type names. It is
// [substrate.OwnershipSurfaceOf], the question this function asked before
// GitHub issue #1118 moved it there: its tag arm is the looser "is there a
// tags or tags_all attribute at all", not [markers.Taggable], and that
// function's doc comment says why.
func markerSurfaceOf(block *configschema.Block) markerSurface {
	surface, _ := substrate.OwnershipSurfaceOf(block)
	return markerSurfaceFor(surface)
}

// markerSurfaceFor is this package's name for a [markers.Surface], and
// surfaceNone for one it has no name for. That last case is the one
// [checkOwnership] admits without a check, so a surface added to
// internal/live/substrate without an arm here would be admitted by
// default; TestMarkerSurfaceForNamesEverySubstrateSurface fails first.
func markerSurfaceFor(surface markers.Surface) markerSurface {
	switch surface {
	case markers.SurfaceTags:
		return surfaceTags
	case markers.SurfaceLabels:
		return surfaceLabels
	case markers.SurfaceManifest:
		return surfaceManifest
	}
	return surfaceNone
}

// surface is the [markers.Surface] s names, "" for surfaceNone.
func (s markerSurface) surface() markers.Surface {
	switch s {
	case surfaceTags:
		return markers.SurfaceTags
	case surfaceLabels:
		return markers.SurfaceLabels
	case surfaceManifest:
		return markers.SurfaceManifest
	}
	return ""
}

// markerCapable reports whether a resource type has anywhere to carry an
// ownership marker, read from the provider's own schema for the type. It is
// the same question [discovery.markerCapable] asks of a list schema, asked of
// the managed resource schema a projection has in hand.
func markerCapable(block *configschema.Block) bool {
	return markerSurfaceOf(block) != surfaceNone
}

// markersOf reads the marker map off the live object the provider handed
// back, from whichever place this type's surface keeps it
// ([substrate.MarkersOf]). The second return is the surface reader's own:
// false means "this object has no such map at all", which is a provider
// bug on a type whose schema declares one and is never a licence to adopt.
//
// On the manifest surface it reads the prior manifest, which
// [mirrorManifestMarker] has already carried the live object's own answer
// for [markers.TagEstate] into - see that function's doc comment, and
// #1079's reason for it: the provider's computed_fields default makes the
// live labels the truth of metadata.labels, so this is where the live
// object's estate label is readable on this shape.
func (s markerSurface) markersOf(obj cty.Value) (map[string]string, bool) {
	return substrate.MarkersOf(s.surface(), obj)
}

// carriesAddress reports whether this surface carries a tofu-address
// marker beside the estate one ([substrate.CarriesAddress]). Only the AWS
// tag map does: #1016's ruling is that the Kubernetes marker is the estate
// label alone, because the object's own group, kind, namespace and name
// are the join key back to configuration and nearly half of real addresses
// are illegal as a label value anyway. So the second half of the ownership
// question ([builder.addressNames]) and the stale-record check that shares
// its rule are asked on the tag surface and nowhere else, rather than
// being asked of a label that is not supposed to exist and reading its
// absence as a finding.
func (s markerSurface) carriesAddress() bool { return substrate.CarriesAddress(s.surface()) }

// carrierPhrase names where the marker map lives, for the one refusal that
// has to tell an operator the provider returned no such map. The tags
// wording is unchanged from before this surface existed.
func (s markerSurface) carrierPhrase() string {
	switch s {
	case surfaceLabels:
		return "metadata.labels map"
	case surfaceManifest:
		return "manifest.metadata.labels map"
	}
	return "tags attribute"
}
