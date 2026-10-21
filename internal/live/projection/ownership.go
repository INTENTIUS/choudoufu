// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"fmt"
	"log"
	"sort"
	"strings"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/armtypes"
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

	// FieldGranularUnheld names the declared field-granular instances on
	// whose object the estate's field manager owns no field of the type
	// (#1885, discovery's Result.FieldGranularUnheld). Each is absent: the
	// plan proposes its create and the provider is never asked to read it.
	FieldGranularUnheld map[string]bool

	// Policy is GitHub issue #67's resolved ownership policy. Nil is today's
	// fixed behavior: a declared+tagged instance converges and a declared+
	// untagged one is refused, exactly as [checkOwnership] read them before
	// Policy existed. Set, it governs both declared quadrants - see
	// [builder.checkPolicy].
	Policy *policy.Policy

	// PendingDisplaced are discovery's displaced-marker warnings that wait
	// on one declared instance's read, keyed by that instance's address
	// (discovery's Result.PendingDisplacedWarnings, GitHub issue #2140,
	// finding 4). Each is about an object at that instance's ARM id
	// carrying this estate's marker for another address, which the read
	// refuses under [SummaryOverwriteMarkedAtKey]. The build emits the
	// warnings of every instance it did not refuse that way, so the object
	// gets exactly one diagnostic whether or not its read ran.
	PendingDisplaced map[string]tfdiags.Diagnostics
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
// [substrate.SurfaceOf]. GitHub issue #1108: the surface read used to be the
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
	return b.checkOwnershipAt(addr, typeName, importID, schema, obj, declared, located, recordFirst, !recordFirst)
}

// checkOwnershipAt is [builder.checkOwnership] with the one fact GitHub issue
// #1546's refusal needs and the caller alone has: atDeclaredKey, true when
// importID is the identity this instance's own configuration computes rather
// than one a record supplied. Every path but the record-first read binds a
// Kubernetes object by the namespace and name its block declares, so
// [builder.checkOwnership] passes !recordFirst; [builder.materialize] passes
// the record-first read's own comparison (see [wanted.declaredKey]). A
// record naming some other key is evidence about an object at THAT key, and
// a create at the declared one would not collide with it.
func (b *builder) checkOwnershipAt(addr addrs.AbsResourceInstance, typeName, importID string, schema providers.Schema, obj cty.Value, declared, located, recordFirst, atDeclaredKey bool) ownershipVerdict {
	own := b.opts.Ownership
	surface, _ := substrate.SurfaceOf(b.providerType(addr, typeName), schema.Block)
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
	case surface == "":
		return ownershipOK
	}

	// GitHub issue #1120's first Azure lane run. azapi_resource's schema has
	// a tags argument for every ARM type, so the surface above is arm-tags
	// for a role assignment too; but whether an object can carry a marker is
	// a property of the ARM type and API version its type argument names,
	// and the stamp writes none on a type that takes no tags
	// ([azapiCarriesNoMarker], the stamp's own armTagTarget question). Such
	// an object's empty tags say nothing about whose it is. Its record does:
	// live-import -approve and an apply's write-back record its identity,
	// the way an untaggable AWS or azurerm type's is recorded, so a record
	// that bound it is the proof, as for a located instance above. Without
	// this the record-first read reported the record stale (no tofu-address
	// to confirm it), the instance fell back to its declared id, and the
	// overwrite refusal below fired on an object this estate had just
	// migrated. With no record the refusal stands, worded for the record
	// path ([builder.overwritesAtKey]) rather than for tags nothing can
	// write.
	markerless := surface == markers.SurfaceARMTags && azapiCarriesNoMarker(schema.Block, obj)
	if markerless && recordFirst {
		return ownershipOK
	}

	tags, taggable := substrate.MarkersOf(surface, obj)
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
			typeName, importID, substrate.CarrierPhrase(surface), markers.TagEstate), noMarkerCause(typeName), substrate.AddressInMarkers(surface), false)
		return ownershipUnowned
	}

	if recordFirst && substrate.AddressInMarkers(surface) {
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

	// overwrites is GitHub issue #2135's widening of #1120's refusal
	// (maintainer ruling, 2026-10-20): on a surface whose create at an
	// occupied id updates the object there ([substrate.CreateOverwritesAtKey]:
	// an ARM create is a PUT), a declared create may proceed only onto an
	// empty id or onto an object carrying this exact block's marker. The two
	// marked readings below - another estate's, and this estate's for
	// another address - left this instance out of the prior state with a
	// warning and a create standing, which on ARM is a PUT over the object
	// they describe. AKS produces both without any choudoufu run writing
	// them: it copies a cluster's tags over those of the existing route
	// table, public IP or virtual network the cluster is given. AWS and
	// Kubernetes keep the warnings, because their surfaces answer false.
	overwrites := declared && own.Estate != "" && atDeclaredKey && substrate.CreateOverwritesAtKey(surface)

	if estate != "" && own.Estate != "" && !tagged && overwrites {
		raw, corrupt := markers.GatherAddress(tags)
		b.overwritesMarkedAtKey(addr, typeName, importID, estate, raw, corrupt, own.Policy.Verb(true, false))
		return ownershipUnowned
	}

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
			noMarkerCause(typeName), substrate.AddressInMarkers(surface), false)
		return ownershipUnowned
	}

	if tagged && substrate.AddressInMarkers(surface) {
		// This estate's marker is on the object, so the second half of the
		// marker spec's ownership question applies: WHICH of this estate's
		// instances is it? GitHub issue #244 - both this layer and discovery
		// deferred that to the other, in comments, and neither performed it.
		//
		// Only on the tag surface: see [substrate.AddressInMarkers]. A
		// Kubernetes object carries its address too, in an annotation
		// outside the label map read here (GitHub issue #1641), and this
		// check does not read it: the natural key the configuration names
		// is that object's identity, and a rename rewrites the annotation
		// in place (#1639).
		if detail, cause, ok := b.addressNames(addr, typeName, importID, tags); !ok {
			if overwrites {
				// The same marker #244 refuses with a warning, on a
				// surface where the create that warning leaves standing
				// is a PUT over this object (GitHub issue #2135).
				raw, corrupt := markers.GatherAddress(tags)
				b.overwritesMarkedAtKey(addr, typeName, importID, estate, raw, corrupt, "")
				return ownershipUnowned
			}
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

	if declared && !nonDefault && estate == "" && own.Estate != "" && atDeclaredKey && substrate.CreateCollidesOnKey(surface) && !adoptsOnCreate(typeName) {
		// GitHub issue #1546, ruled 2026-09-26: refuse, narrowly. Both of
		// the ruling's halves hold by the time control is here.
		//
		// The object was positively read on the cluster this run. Reaching
		// this line means [builder.materialize] got a materialized status
		// (an absence returns before ownership is asked, and so does a
		// failed read), and a state-cache answer never gets this far: the
		// cache serves only an instance the sweep verified or the record
		// envelope vouched, and both of those return ownershipOK above.
		// The labels map was read off that object and holds no
		// tofu-estate at all - not another estate's, which returned above.
		//
		// And the create this configuration declares would be refused by
		// the API server: see [substrate.CreateCollidesOnKey] for which
		// shapes that is true of, and atDeclaredKey for why the key read is
		// the key the create would send.
		//
		// Only under the default verb. "adopt" and "converge" admitted the
		// object above; "keep" and "report" are an operator's explicit
		// choice of a softer refusal and keep their meaning.
		b.conflictsOnKey(addr, typeName, importID)
		return ownershipUnowned
	}

	if declared && estate == "" && own.Estate != "" && atDeclaredKey && substrate.CreateOverwritesAtKey(surface) {
		// GitHub issue #1120, ruled on #1122: the opposite server and the
		// same refusal. Everything the #1546 branch above establishes holds
		// here too - the object was positively read at the identity this
		// block's own configuration computes and it carries no tofu-estate
		// at all - but this surface's create does not fail at a held key,
		// it updates the object there ([substrate.CreateOverwritesAtKey]:
		// an ARM create is a PUT). The warning the default below raises
		// would then sit beside a plan whose apply either takes the object
		// over or, where the provider reads the id first, fails partway
		// through ([overwriteProviderGuard] says which, per provider), so
		// the run stops here instead, by name, before any create is
		// proposed.
		//
		// Under every verb that reaches this line, not only the default:
		// refuse, keep and report. adopt and converge never reach it.
		// #1546's branch keeps "keep" and "report" as softer refusals
		// because there the server refuses the create before anything is
		// changed; here those two verbs leave the object out of the prior
		// state exactly as refuse does and the plan proposes the create,
		// whose only guard against the takeover is then a provider's own
		// pre-read, which azurerm lets a features flag turn off and does
		// not do for every type - "keep" would even have proposed it
		// without a warning.
		// The verbs that admit the object (adopt, converge) returned
		// above, and they are the deliberate route: the object enters the
		// prior state and the plan writes the marker onto it in the open.
		// internal/live/lint's policy-overwrite-at-key refuses the two
		// verbs before any plan for an estate this reaches; this is the
		// projection's own guard, for a run that bypassed lint.
		b.overwritesAtKey(addr, typeName, importID, verb, markerless, obj)
		return ownershipUnowned
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
	case estate == "" && !substrate.AddressInMarkers(surface):
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
	b.unowned(addr, typeName, importID, estate, detail, noMarkerCause(typeName), substrate.AddressInMarkers(surface), nonDefault && verb == policy.Keep)
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

// SummaryNameHeld is the Error GitHub issue #1546's refusal carries.
const SummaryNameHeld = "Unlabelled live object holds the declared name"

// conflictsOnKey records #1546's refusal: the same Unowned entry and
// omission [builder.unowned] records, under an Error of its own instead of
// the [SummaryOutsideEstate] warning. An Error, because the plan the warning
// used to sit beside proposed a create the API server answers with 409
// AlreadyExists, and a plan that is wrong before the server sees it is one a
// human should be stopped at rather than asked to approve.
func (b *builder) conflictsOnKey(addr addrs.AbsResourceInstance, typeName, importID string) {
	own := b.opts.Ownership
	detail := fmt.Sprintf(
		"A live %s already exists at %q, the namespace and name %s declares, and carries no %s label, so this estate does not own it. The API server keys this object by its kind, namespace and name, so the create this configuration declares would be refused with 409 AlreadyExists while that object holds the name; this plan stops here instead of proposing it. To adopt the object, set policy { declared_untagged = \"adopt\" } in the live block and re-run, or write the label %s=%q onto it and re-run. To keep it out of this estate, point this resource at a name nobody is using.",
		typeName, importID, addr, markers.TagEstate, markers.TagEstate, own.Estate)
	b.unownedList = append(b.unownedList, Unowned{
		Addr:     addr,
		TypeName: typeName,
		ImportID: importID,
		Detail:   detail,
	})
	b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryNameHeld, detail))
	b.omit(addr, ReasonUnowned, detail, noMarkerCause(typeName))
}

// SummaryOverwriteAtKey is the Error GitHub issue #1120's create-or-update
// refusal carries.
const SummaryOverwriteAtKey = "Unmarked live object at the declared id would be overwritten"

// overwritesAtKey records #1120's refusal ([substrate.CreateOverwritesAtKey]):
// the Unowned entry, the omission, and an Error, the shape
// [builder.conflictsOnKey] records for the opposite server.
//
// verb is the declared_untagged verb this run resolved, which is refuse,
// keep or report by the time this is called (adopt and converge admit the
// object instead). A verb other than the default is named in the message,
// because the operator asked for something in writing and is owed the
// reason it did not apply.
//
// The message says what the resource's own provider would do at apply
// ([overwriteProviderGuard]), because ARM's PUT is only half the story:
// both providers the family covers read the id before that PUT in their
// default configuration, so the refusal is usually earlier than an error
// the apply would raise anyway, not the only thing between the plan and a
// takeover (GitHub issue #2133, finding 5). It stays on both, for the
// reasons that function gives.
//
// markerless is an azapi object whose ARM type takes no tags at its API
// version ([azapiCarriesNoMarker]), read with no record binding it. No tag
// can be on it and none can be written, so the message speaks of the record
// that would be its only carrier, and sends the operator to the two routes
// that write one (adopt, and live-import -approve), never to tagging it.
func (b *builder) overwritesAtKey(addr addrs.AbsResourceInstance, typeName, importID string, verb policy.Verb, markerless bool, obj cty.Value) {
	own := b.opts.Ownership
	var asked string
	if verb != "" && verb != policy.DefaultVerb[policy.DeclaredUntagged] {
		asked = fmt.Sprintf(" policy { %s = %q } does not change this: that verb leaves the object out of this estate and lets the plan propose the create, which is the PUT above.", policy.DeclaredUntagged.Attribute(), verb)
	}
	if markerless {
		b.overwritesUnrecordedAtKey(addr, typeName, importID, asked, obj)
		return
	}
	detail := fmt.Sprintf(
		"A live %s already exists at %q, the id %s declares, and carries no %s tag, so this estate does not own it. Creating this resource is a PUT to that id, and ARM answers a PUT to an id that already exists by updating the object there instead of failing. %s%s To adopt the object, set policy { declared_untagged = \"adopt\" } in the live block and re-run, or write the tags %s=%q and %s=%q onto it and re-run. To keep it out of this estate, point this resource at a name nobody is using. This is #1546's refusal for a server that overwrites instead of refusing (GitHub issue #1120).",
		typeName, importID, addr, markers.TagEstate,
		overwriteProviderGuard(b.providerType(addr, typeName)), asked,
		markers.TagEstate, own.Estate,
		markers.TagAddress, markers.EscapeAddress(addr.String()))
	b.unownedList = append(b.unownedList, Unowned{
		Addr:          addr,
		TypeName:      typeName,
		ImportID:      importID,
		AddressMarker: true,
		Detail:        detail,
	})
	b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryOverwriteAtKey, detail))
	b.omit(addr, ReasonUnowned, detail, noMarkerCause(typeName))
}

// overwritesUnrecordedAtKey is [builder.overwritesAtKey]'s message for a
// markerless azapi object: the same Unowned entry, omission and Error under
// [SummaryOverwriteAtKey], with the record, not a tag, as the missing
// carrier. AddressMarker is false: no tofu-address can be written onto it.
func (b *builder) overwritesUnrecordedAtKey(addr addrs.AbsResourceInstance, typeName, importID, asked string, obj cty.Value) {
	own := b.opts.Ownership
	armType, apiVersion := "(a sensitive type)", "(unknown)"
	if typ := obj.GetAttr(azapiTypeName); !typ.IsMarked() {
		armType, apiVersion, _ = armtypes.ParseAzapiType(typ.AsString())
	}
	var record string
	if b.opts.RecordStore == nil {
		record = "Its identity record is the only thing that could say this estate owns it, and this live block declares no record_store, so there is nowhere to keep one: add a record_store to the live block first."
	} else {
		record = fmt.Sprintf("Its identity record in this estate's record store is the only thing that could say this estate owns it, and there is none for %s.", addr)
	}
	detail := fmt.Sprintf(
		"A live %s already exists at %q, the id %s declares. Its ARM type, %s at API version %s, takes no tags, so it cannot carry a %s marker and none is looked for. %s Creating this resource is a PUT to that id, and ARM answers a PUT to an id that already exists by updating the object there instead of failing. %s%s To adopt the object, set policy { declared_untagged = \"adopt\" } in the live block and re-run, and the apply records it; or, if a stock state file manages it, run choudoufu live-import -state=<that file> -estate=%s -approve, which writes its record. To keep it out of this estate, point this resource at a name nobody is using. This is #1546's refusal for a server that overwrites instead of refusing (GitHub issue #1120).",
		typeName, importID, addr, armType, apiVersion, markers.TagEstate, record,
		overwriteProviderGuard(b.providerType(addr, typeName)), asked,
		own.Estate)
	b.unownedList = append(b.unownedList, Unowned{
		Addr:     addr,
		TypeName: typeName,
		ImportID: importID,
		Detail:   detail,
	})
	b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryOverwriteAtKey, detail))
	b.omit(addr, ReasonUnowned, detail, fmt.Sprintf("the live %s at its declared id has no record in this estate's record store, and its ARM type carries no marker.", typeName))
}

// azapiCarriesNoMarker reports whether obj, an object of a block with
// azapi_resource's shape, names in its type argument an ARM type that takes
// no tags at that API version, or one this build does not know: the objects
// the stamp writes no marker on ([armTagTarget]). False for any other block,
// and for an object whose type cannot be read.
func azapiCarriesNoMarker(block *configschema.Block, obj cty.Value) bool {
	if !azapiShaped(block) || obj == cty.NilVal || obj.IsNull() || !obj.IsKnown() || !obj.Type().IsObjectType() || !obj.Type().HasAttribute(azapiTypeName) {
		return false
	}
	typ := obj.GetAttr(azapiTypeName)
	if typ.IsMarked() || typ.IsNull() || !typ.IsKnown() || typ.Type() != cty.String {
		return false
	}
	return !substrate.AzapiTypeCarriesMarker(typ.AsString())
}

// SummaryOverwriteMarkedAtKey is the Error GitHub issue #2135's widening of
// #1120's refusal carries: the object at the declared id is marked, but not
// for this block.
const SummaryOverwriteMarkedAtKey = "Live object marked for another owner at the declared id would be overwritten"

// overwritesMarkedAtKey records GitHub issue #2135's refusal (maintainer
// ruling, 2026-10-20): a declared resource on a surface whose create
// overwrites ([substrate.CreateOverwritesAtKey]) was read at the id its own
// configuration computes, and the object there carries an ownership marker
// that is not this block's - another estate's tofu-estate, or this
// estate's with a tofu-address naming another instance (or a malformed
// one). Before the ruling those were #1166's and #244's warnings, each
// beside a plan proposing the create, which on ARM is a PUT that writes this
// block's markers over the object's.
//
// The Unowned entry, the omission and an Error, the shape
// [builder.overwritesAtKey] records for an unmarked object. A separate
// summary because the operator's question is different: an unmarked object
// is somebody's that nobody claimed, and adopting it is one policy verb
// away; a marked one is claimed, and the message has to say by whom and how
// a claim nobody here wrote got there.
//
// verb is declared_untagged's verb for an object of another estate, named
// when it is not the default, for [anotherEstateDetail]'s reason: the
// operator asked for something in writing and is owed why it did not reach
// this object. It is "" for this estate's own object, which no
// declared_untagged verb reaches either way.
func (b *builder) overwritesMarkedAtKey(addr addrs.AbsResourceInstance, typeName, importID, estate, rawAddress string, corrupt bool, verb policy.Verb) {
	own := b.opts.Ownership
	if b.refusedMarkedAtKey == nil {
		b.refusedMarkedAtKey = map[string]bool{}
	}
	b.refusedMarkedAtKey[addr.String()] = true
	escaped := markers.EscapeAddress(addr.String())

	var found string
	switch {
	case corrupt:
		found = fmt.Sprintf("%s=%q and %s continuation tags with a gap in them, so the address it names cannot be read", markers.TagEstate, estate, markers.TagAddress)
	case rawAddress == "":
		found = fmt.Sprintf("%s=%q and no %s", markers.TagEstate, estate, markers.TagAddress)
	default:
		found = fmt.Sprintf("%s=%q and %s=%q", markers.TagEstate, estate, markers.TagAddress, rawAddress)
	}
	var whose string
	if estate == own.Estate {
		whose = fmt.Sprintf("That is this estate's marker for another address than %s, so the object is claimed by a different instance of this estate, or by none.", addr)
	} else {
		whose = fmt.Sprintf("That is estate %q's marker, not this estate's (%q).", estate, own.Estate)
	}
	var asked string
	if verb != "" && verb != policy.DefaultVerb[policy.DeclaredUntagged] {
		asked = fmt.Sprintf(" policy { %s = %q } does not reach it: that quadrant is about objects carrying no estate marker at all, and this one is marked.", policy.DeclaredUntagged.Attribute(), verb)
	}

	var remedies string
	if estate == own.Estate {
		remedies = fmt.Sprintf(
			"If AKS copied the tags, move this resource's ownership off its tags: strict { markers \"record\" { types = [%q] } } in the live block holds its identity in the estate's record store, and the copied tags then decide nothing; or keep it out of this estate, or in a resource group no cluster is given. If the resource really moved, say so with a moved block or with choudoufu live-mv. If the tags are simply wrong, write %s=%q onto the object by hand and re-run.",
			typeName, markers.TagAddress, escaped)
	} else {
		remedies = fmt.Sprintf(
			"If a cluster of estate %q copied its tags here, move this resource's ownership off its tags: strict { markers \"record\" { types = [%q] } } in the live block holds its identity in the estate's record store; or keep it out of this estate, or in a resource group no cluster is given. To take the object over deliberately, `live-mv -from-estate=%s` rewrites the marker to this estate one address at a time, and `live-import -approve` stamps this estate's markers over the resources a state file lists. Or point this resource at a name nobody is using.",
			estate, typeName, estate)
	}

	detail := fmt.Sprintf(
		"A live %s already exists at %q, the id %s declares, and it carries %s. %s%s Creating this resource is a PUT to that id, and ARM answers a PUT to an id that already exists by updating the object there instead of failing, so the create would write this block's markers over that object's. %s Markers this estate did not write usually come from one of two places: AKS copies a cluster's tags, the markers this estate stamped on the cluster among them, over the tags of the existing resources the cluster is given (its route table, public IP, virtual network and the rest) while the cluster exists, and removes them when it is deleted; and an Azure Policy assignment with a modify effect writes tag values onto the resources in its scope. %s This is #1120's overwrite refusal for an object that is marked, but not for this block (GitHub issue #2135).",
		typeName, importID, addr, found, whose, asked,
		overwriteProviderGuard(b.providerType(addr, typeName)),
		remedies)
	b.unownedList = append(b.unownedList, Unowned{
		Addr:          addr,
		TypeName:      typeName,
		ImportID:      importID,
		Estate:        estate,
		AddressMarker: true,
		Detail:        detail,
	})
	b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, SummaryOverwriteMarkedAtKey, detail))
	cause := fmt.Sprintf("the live %s at its identity carries another owner's marker and would be overwritten.", typeName)
	b.omit(addr, ReasonUnowned, detail, cause)
}

// overwriteProviderGuard is the sentence [builder.overwritesAtKey] says
// about what providerType, the resource's provider, does when its create
// finds an object already at the id, and why this plan refuses anyway.
// GitHub issue #2133, finding 5, answered from each provider's source at
// the pinned version:
//
//   - hashicorp/azurerm 5.10.0 reads the id before the PUT and fails the
//     create with tf.ImportAsExistsError ("a resource with the ID %q
//     already exists - to be managed via Terraform this resource needs to
//     be imported into the State", helpers/tf/errors.go:9-12), e.g.
//     internal/services/resource/resource_group_resource.go:76-87. The read
//     is skipped when the provider block's features block sets
//     skip_import_check_on_create_and_allow_overwriting_existing_resources
//     = true (internal/provider/features.go:26-31 and 544-546, default
//     false), and a few create functions do no such read at all: 61 of the
//     1,086 *_resource.go files under internal/services name neither the
//     error nor the flag, among them
//     containerapps/container_app_environment_certificate_resource.go:164-213,
//     a taggable type whose create is a bare CreateOrUpdate. So under
//     azurerm the refusal is usually earlier than an apply-time error and
//     occasionally the only guard.
//   - azure/azapi 2.13.0's azapi_resource, the one azapi type with a
//     top-level tags map, reads the id before every create
//     (internal/services/azapi_resource.go:873-893, CreateUpdate's
//     isNewResource branch) and fails with "Resource already exists"
//     (tf.ImportAsExistsError) when the read succeeds. Nothing turns it off:
//     the branch is conditioned on isNewResource alone, with no provider
//     feature and no ignore_* argument consulted, and only a not-found (or a
//     403 at management-group scope) lets the create proceed. So under azapi
//     the refusal is always earlier than the apply-time error, never the only
//     guard.
//
// The refusal stays on both because it is a plan-time stop: an apply-time
// "already exists" comes after the apply has created whatever was ordered
// before this resource, and leaves the operator a half-applied estate to
// reason about; the plan-time one leaves nothing applied, and its
// remedies (adopt, or tag the object) are this estate's, where the
// provider's says to import it into a state file this estate does not
// keep.
func overwriteProviderGuard(providerType string) string {
	const first = "This plan stops here, before anything is applied, instead of proposing the create."
	switch providerType {
	case "azurerm":
		return "hashicorp/azurerm reads the id before that PUT and fails the apply with \"a resource with the ID ... already exists\" for most of its types, but not when the provider block's features block sets skip_import_check_on_create_and_allow_overwriting_existing_resources = true, and not for the few types whose create does no such read, where the apply would take the object over. " + first + " It refuses before azurerm would, so that neither that setting nor the type decides whether the object is taken over, and so that the failure comes before any other resource in this apply has been created."
	case "azapi":
		return "azure/azapi reads the id before that PUT and fails the apply with \"Resource already exists\", with no setting that turns the read off. " + first + " It refuses before azapi would, so the failure comes before any other resource in this apply has been created, rather than partway through the apply."
	default:
		return "Nothing in this provider is known to read the id first, so the apply would take that object over with nothing to say it was ever this estate's. " + first
	}
}

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
		// [substrate.AddressInMarkers].
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

// The marker surface a type carries is [markers.Surface], read off the
// provider's own schema by [substrate.SurfaceOf], never off a list
// of type names. GitHub issue #1108: until the surface read learned a
// second carrier it returned "no surface" for every Kubernetes type, so
// [builder.checkOwnership] admitted a Kubernetes object without ever
// reading its label - another estate's object relabelled by the plan, an
// unlabelled one adopted in silence, and the ownership policy's verbs never
// reached on that substrate at all. GitHub issue #1589: the ownership read
// used to ask a looser tag question than [substrate.SurfaceOf] here
// (the since-deleted markers.HasTagsAttribute, any "tags" or "tags_all" attribute at all,
// settable or not), kept apart in case a real type ever needed the wider
// reading. The 2026-09-26 decision package measured the two questions
// agreeing on every type in the pinned AWS and Kubernetes schemas, so the
// ownership read now asks [substrate.SurfaceOf] like every other caller.
//
// GitHub issue #1584: this package used to keep its own enum for the
// surfaces, with the per-surface answers (which carries an address, which
// collides on its key, what the carrier is called) switching on it. The
// answers now live on [substrate.Substrate], asked with the one
// [markers.Surface] value, so a surface is declared once.

// adoptsOnCreate reports whether typeName follows the providers'
// "<provider>_default_<kind>" convention for a type whose create adopts an
// object the platform already made instead of creating one - the same
// convention internal/live/discovery's defaultAdopterPrefix keys
// aws_default_* on. On Kubernetes that is kubernetes_default_service_account
// and its _v1 spelling: the create waits for the namespace's "default"
// ServiceAccount and updates it, so it never meets the 409 GitHub issue
// #1546's refusal exists to get ahead of, and refusing it would refuse a
// configuration that works.
func adoptsOnCreate(typeName string) bool {
	_, rest, ok := strings.Cut(typeName, "_")
	return ok && strings.HasPrefix(rest, "default_")
}

// pendingDisplaced is [Ownership.PendingDisplaced]'s warnings for every
// instance this build did not refuse under [SummaryOverwriteMarkedAtKey],
// in address order: the object's one diagnostic when its read did not run
// or did not refuse it (GitHub issue #2140, finding 4).
func (b *builder) pendingDisplaced() tfdiags.Diagnostics {
	own := b.opts.Ownership
	if own == nil || len(own.PendingDisplaced) == 0 {
		return nil
	}
	keys := make([]string, 0, len(own.PendingDisplaced))
	for k := range own.PendingDisplaced {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var diags tfdiags.Diagnostics
	for _, k := range keys {
		if b.refusedMarkedAtKey[k] {
			continue
		}
		diags = diags.Append(own.PendingDisplaced[k])
	}
	return diags
}
