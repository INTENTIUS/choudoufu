// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package liveimport

import (
	"context"
	"fmt"
	"sort"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/discovery"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Providers supplies a configured provider instance for a provider
// configuration address - the same seam
// [github.com/intentius/choudoufu/internal/live/projection.Providers] and the
// command package's statelessProviders both implement, narrowed to the one
// method this package calls. Nothing here lists a resource type and nothing
// here evaluates a provider block a second time; state already gives every
// resource its identity.
type Providers interface {
	ConfiguredProvider(ctx context.Context, addr addrs.AbsProviderConfig) (providers.Interface, error)
}

// Status is the ratification verdict for one resource instance. Every value
// is one of the five the issue names; nothing in this package produces a
// sixth.
type Status string

const (
	// StatusVerified means this run read the identity the state recorded off
	// the live system and every attribute matched. Eligible for stamping.
	StatusVerified Status = "VERIFIED"

	// StatusMissing means the live system could not confirm the identity the
	// state recorded - a real deletion, a provider or schema this run could
	// not use, or a read call that failed; Detail says which. Never
	// stamped: there is nothing live to carry a marker.
	StatusMissing Status = "MISSING"

	// StatusDrifted means the live system answered, but at least one
	// attribute besides tags_all (the provider's own computed rollup)
	// differs from what the state recorded. Reported, not fatal: the
	// resource is still eligible for stamping, the same way a plan still
	// proposes reconciling drift on a resource whose ownership is already
	// settled.
	StatusDrifted Status = "DRIFTED"

	// StatusUnadmittedType means the resource type has no row in the
	// identity package's admission table, so this package has no knowledge
	// of how to read or write it at all. Never stamped.
	StatusUnadmittedType Status = "UNADMITTED_TYPE"

	// StatusUntaggable means the provider's schema for the type has no tags
	// argument of the shape live/MARKERS.md describes, so there is nowhere
	// on the live object to write a marker. Never stamped, and not a
	// problem in itself - see the stamp package's doc comment on why an
	// untaggable type is not a gap in an estate's records.
	//
	// A record-backed type reaches this status too, and for a stronger
	// reason: it has no live object at all. What separates the two is what
	// Approve then does - see [recordable] and GitHub issue #340. There is
	// deliberately no sixth Status for it, because the ratification verdict
	// is the same one ("nowhere to write a marker"); what differs is the
	// carrier, which is Approve's business, not Ratify's.
	//
	// The same is true a third time for GitHub issue #341: an ordinary,
	// admitted, untaggable cloud resource keeps this exact verdict and still
	// has its argument residue recorded, through [residuable]. This status
	// answers "is there anywhere to write a marker" and nothing else. Reading
	// it as "there is nothing this run owes this resource" is the mistake
	// #341 was.
	StatusUntaggable Status = "UNTAGGABLE"
)

// Entry is one resource instance's ratification verdict, safe to hand to a
// view: nothing on it needs a live provider connection to render.
type Entry struct {
	Addr     addrs.AbsResourceInstance
	TypeName string
	Status   Status
	Detail   string

	// LiveID is the identity attribute value this run read the object by,
	// when one is known. Empty when nothing was attempted (StatusUnadmittedType)
	// or no identity attribute could be read.
	LiveID string

	// Drifted names the top-level attributes whose live value differs from
	// the state's recorded value, each rendered "name (state -> live)". Set
	// only when Status is StatusDrifted.
	Drifted []string
}

// residuable is what Approve needs to classify and record ONE instance's
// argument residue - GitHub issue #275's class, through
// [projection.RecordResidueForInstance]: a live provider connection, the
// type's schema, and the object whose argument values are under test.
//
// It is its own carrier rather than a field of [eligible], and GitHub issue
// #341 is what happened while it was not. "Write a marker" and "record what
// we sent" are different jobs with different preconditions - the first needs
// a tags argument in the provider's schema, the second needs nothing but a
// provider and an object - and while one carrier gated both, an untaggable
// resource migrated with no residue record at all. That is not an edge: of
// [identity.DefaultTable]'s 1040 rows, live/survey-full.json's own taggable
// signal calls 683 taggable and 342 untaggable (15 rows it does not cover at
// all), and the whole association/attachment/membership family is in the 342.
//
// Nothing here reads that artifact, and nothing may. The gate is
// [taggable] over the schema the provider itself served this run, which is
// what decides the 15 too.
//
// applied is already unmarked, because a marked leaf cannot cross the plugin
// channel and cannot be encoded into a record.
type residuable struct {
	provider providers.Interface
	schema   providers.Schema
	typeName string

	// applied is the object this instance's arguments are classified from.
	//
	// For a stampable instance it is the live object [Ratify] just read - the
	// same value the tag write is planned against. For an untaggable one it
	// is the state's own recorded object, decoded against the provider's
	// current schema, which is the same source [projection.WriteBack] takes
	// an applied object from after an apply. That is deliberate rather than a
	// shortcut: a residue attribute is by definition one no read ever returns,
	// so the value the last apply sent is the only value there is, and the
	// state file is where it is written down.
	applied cty.Value

	identity cty.Value
	private  []byte
}

// eligible is what Approve needs for one resource beyond what its Entry
// already reports: the live connection and the freshly read object, carried
// forward from Ratify so Approve never reads the tfstate or the live
// system's identity a second time - only the write itself is a new call.
//
// It embeds [residuable] because every stampable instance is also a residue
// subject; the reverse is not true, which is the whole of issue #341.
type eligible struct {
	residuable
}

// recordable is [eligible]'s sibling for a record-backed instance: what
// Approve needs to seed the estate's record store for one resource whose
// value has no live object to hang off, carried forward from Ratify the same
// way and for the same reason.
//
// The two are disjoint by construction. A resource is eligible when the live
// system can be asked about it and its schema offers somewhere to write a
// tag; it is recordable when [identity.TypeIdentity.RecordBacked] says the
// record IS the identity, which is precisely the case where neither of those
// is true. Nothing is ever both, and GitHub issue #340 is what happened while
// only the first of the two existed: a migrated estate's random_pet,
// null_resource, terraform_data and local_file were reported SKIPPED and
// their generated values were dropped on the floor, so the first live-plan
// after a clean migrate proposed creating every one of them from nothing.
//
// value is already unmarked (see [ratifyRecordBacked]).
type recordable struct {
	typeName string
	value    cty.Value
	private  []byte
	status   states.ObjectStatus
}

// Request is one ratification pass.
type Request struct {
	// Estate is the estate this run would stamp, matching the tofu-estate
	// marker grammar. Required: unlike live-plan and live-mv, there is no
	// configuration here to derive it from if it is left empty.
	Estate string

	// State is the state to read, already parsed by the caller - through
	// [github.com/intentius/choudoufu/internal/states/statefile] - and never
	// touched again by this package. Every module's managed resources are
	// considered, root and child alike; see the package doc, "Scope (v1)".
	State *states.State

	// Providers supplies a configured provider per provider configuration
	// address, keyed exactly as [states.Resource.ProviderConfig] names it.
	Providers Providers

	// ResidueStore is GitHub issue #275's argument-level residue store,
	// opened for this estate exactly the way live-plan and a stateless apply
	// open theirs - nil when the configuration declares no record_store.
	// See [projection.RecordResidueForInstance]'s doc comment (issue #327)
	// for why Approve, not Ratify, is where this gets used: recording
	// residue is a write, and this package's whole contract is that Ratify
	// never writes anything.
	ResidueStore *projection.ResidueStore

	// RecordStore is GitHub issue #73's record store itself - the same one
	// ResidueStore is layered over, opened by the same caller from the same
	// record_store block - and RecordKeyPrefix is the namespace that block
	// resolves to ([projection.RecordStoreKeyPrefix]).
	//
	// Issue #340: a record-backed instance has no live object to tag, so its
	// whole migration IS this write. Nil - no live block, or a live block
	// with no record_store - makes Approve leave every record-backed
	// instance SKIPPED exactly as it did before, which is the right answer
	// there: without a store, internal/live/lint does not admit a
	// record-backed type for planning either.
	RecordStore staterecord.Store

	// RecordKeyPrefix is the key namespace RecordStore's records live under.
	// Ignored when RecordStore is nil.
	RecordKeyPrefix string

	// RootOutputStore is GitHub issue #349's root-output namespace, a sixth
	// namespace in ordinarily the same store the other three views are
	// layered over, opened by the same caller from the same record_store
	// block.
	//
	// A migration is where an estate's remembered output values come from,
	// and it is the only place they can come from for an estate that has
	// never been applied by choudoufu: the stock state file this run is
	// pointed at holds them, and nothing else on this side does. Nil - no
	// live block, or a live block with no record_store - leaves every root
	// output with no prior value, exactly as before, which renders as
	// "+ name = ..." on the next stateless plan.
	//
	// Like the record and residue stores it is used by Approve and not by
	// Ratify: writing is what Approve is for.
	RootOutputStore *projection.RootOutputStore
}

// Ratification is one pass's read-only findings, plus what a later Approve
// call needs to act on them. Entries is safe to render on its own - the rest
// is unexported, so that "the report comes first" is a property of this
// type's shape and not only of the order a caller happens to call things in.
type Ratification struct {
	Estate  string
	Entries []Entry

	eligible   map[string]*eligible
	recordable map[string]*recordable

	// residuable holds the instances that are NOT stampable but still have
	// arguments worth recording - GitHub issue #341. A stampable instance is
	// deliberately not in here: its carrier is its *eligible, which embeds
	// the same thing.
	residuable map[string]*residuable

	residueStore *projection.ResidueStore

	recordStore     staterecord.Store
	recordKeyPrefix string

	// rootOutputStore and rootOutputs are GitHub issue #349's migration
	// half. A stock state file holds the value every root-level `output`
	// block settled on at the last apply, and until this existed a migration
	// dropped every one of them: HANDOFF.md's "migration from a stock state
	// file is lossless" had a hole in it exactly the size of the estate's
	// outputs, and the next stateless plan rendered all of them as newly
	// created.
	//
	// rootOutputs is the whole state rather than the values, because
	// [projection.WriteRootOutputValues] takes a state and reads the
	// sensitivity flag off each [states.OutputValue] itself - and the state
	// is the only place that flag exists here, there being no configuration
	// in hand during a migration.
	rootOutputStore *projection.RootOutputStore
	rootOutputs     *states.State
}

// Ratify reads every managed resource instance in req.State - root module
// and child module alike - and verifies it against the live system through
// req.Providers, producing one Entry per instance. It writes nothing: every
// call it makes is GetProviderSchema or ReadResource, both read-only on the
// provider protocol.
func Ratify(ctx context.Context, req Request) (*Ratification, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	switch {
	case !discovery.ValidEstateName(req.Estate):
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid estate name",
			fmt.Sprintf("A migration needs the estate's name, matching the tofu-estate marker grammar in live/MARKERS.md (a lowercase letter followed by lowercase letters, digits or hyphens, at most 128 characters). Got %q.", req.Estate),
		))
	case req.State == nil:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No state to ratify",
			"live-import needs a parsed tfstate to read, and none was given.",
		))
	case req.Providers == nil:
		return nil, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider access",
			"Verifying a resource against the live system needs a configured provider, and none was given.",
		))
	}

	rat := &Ratification{
		Estate:          req.Estate,
		eligible:        make(map[string]*eligible),
		recordable:      make(map[string]*recordable),
		residuable:      make(map[string]*residuable),
		residueStore:    req.ResidueStore,
		recordStore:     req.RecordStore,
		recordKeyPrefix: req.RecordKeyPrefix,
		rootOutputStore: req.RootOutputStore,
		rootOutputs:     req.State,
	}

	for _, mod := range sortedModules(req.State) {
		for _, res := range sortedResources(mod) {
			if res.Addr.Resource.Mode != addrs.ManagedResourceMode {
				continue
			}
			for _, key := range sortedInstanceKeys(res) {
				addr := res.Addr.Instance(key)
				entry, car := ratifyOne(ctx, req, res, addr, res.Instances[key])
				rat.Entries = append(rat.Entries, entry)
				if car.eligible != nil {
					rat.eligible[addr.String()] = car.eligible
				}
				if car.recordable != nil {
					rat.recordable[addr.String()] = car.recordable
				}
				if car.residuable != nil {
					rat.residuable[addr.String()] = car.residuable
				}
			}
		}
	}

	return rat, diags
}

// carriers is what one instance hands Approve beyond its Entry. At most one
// field is ever set, and which one is the whole of what a migration means for
// that instance: stamp a marker onto it ([eligible]), seed a record for it
// ([recordable]), or record only what its arguments sent ([residuable]).
//
// A struct rather than a widening list of return values, so that a new
// carrier cannot be added by silently appending a nil to twelve return
// statements.
type carriers struct {
	eligible   *eligible
	recordable *recordable
	residuable *residuable
}

// ratifyOne is the whole verdict for one resource instance: admission, the
// schema, the state's own recorded object, and - past every gate that would
// make a live call meaningless - one ReadResource call.
func ratifyOne(ctx context.Context, req Request, res *states.Resource, addr addrs.AbsResourceInstance, inst *states.ResourceInstance) (Entry, carriers) {
	typeName := res.Addr.Resource.Type
	entry := Entry{Addr: addr, TypeName: typeName}

	if _, admitted := identity.LookupType(typeName); !admitted && !admittedByProviderSchema(ctx, req, res, typeName) {
		entry.Status = StatusUnadmittedType
		entry.Detail = fmt.Sprintf(
			"There is no identity knowledge for resource type %q, so this run cannot read or verify it. See live/LIMITATIONS.md, \"unadmitted-type\", for the admitted set.",
			typeName)
		return entry, carriers{}
	}

	if inst == nil || inst.Current == nil {
		entry.Status = StatusMissing
		entry.Detail = "The state has no current object for this instance - only a deposed one, or none at all - so there is nothing recorded to verify against the live system."
		return entry, carriers{}
	}

	providerAddr := impliedProviderAddr(res)
	provider, err := req.Providers.ConfiguredProvider(ctx, providerAddr)
	if err != nil {
		entry.Status = StatusMissing
		entry.Detail = fmt.Sprintf("Provider %s could not be used, so this instance could not be verified: %s.", providerAddr, err)
		return entry, carriers{}
	}

	schema, schemaErr := resourceSchema(ctx, provider, typeName)
	if schemaErr != nil {
		entry.Status = StatusMissing
		entry.Detail = schemaErr.Error()
		return entry, carriers{}
	}

	// Issue #340, and deliberately ahead of the taggability check: a
	// record-backed instance is untaggable for a stronger reason than a
	// missing tags argument - there is no live object at all - and the
	// verdict it would reach below is the right one. What it must not do is
	// fall off the end of a migration with nothing written anywhere, which
	// is what happened while this branch did not exist.
	if recordBackedType(typeName) && req.RecordStore != nil {
		return ratifyRecordBacked(entry, typeName, schema, inst)
	}

	if !taggable(schema.Block) {
		return ratifyUntaggable(entry, provider, schema, typeName, inst)
	}

	prior, decErr := inst.Current.Decode(schema.Block.ImpliedType())
	if decErr != nil {
		entry.Status = StatusMissing
		entry.Detail = fmt.Sprintf("The recorded state does not fit the provider's current schema for %s: %s.", typeName, decErr)
		return entry, carriers{}
	}
	priorVal, _ := prior.Value.UnmarkDeep()
	entry.LiveID = liveIDFrom(typeName, priorVal)

	readResp := provider.ReadResource(ctx, providers.ReadResourceRequest{
		TypeName:      typeName,
		PriorState:    priorVal,
		Private:       prior.Private,
		ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
		PriorIdentity: prior.Identity,
	})
	if readResp.Diagnostics.HasErrors() {
		entry.Status = StatusMissing
		entry.Detail = fmt.Sprintf("The provider failed while reading this resource from the live system: %s.", readResp.Diagnostics.Err())
		return entry, carriers{}
	}
	if readResp.NewState == cty.NilVal || readResp.NewState.IsNull() {
		entry.Status = StatusMissing
		entry.Detail = "The live system reports that this identity no longer exists."
		return entry, carriers{}
	}

	if drifted := driftedAttrs(schema.Block, priorVal, readResp.NewState); len(drifted) > 0 {
		entry.Status = StatusDrifted
		entry.Drifted = drifted
		entry.Detail = fmt.Sprintf("The live object differs from the state's recorded attributes: %s. Verification does not fix drift; a live-plan after stamping will show it.", joinList(drifted))
	} else {
		entry.Status = StatusVerified
		entry.Detail = "The live object matches the state's recorded attributes."
	}

	return entry, carriers{eligible: &eligible{residuable{
		provider: provider,
		schema:   schema,
		typeName: typeName,
		applied:  readResp.NewState,
		identity: readResp.NewIdentity,
		private:  readResp.Private,
	}}}
}

// ratifyUntaggable is ratifyOne's verdict for an ADMITTED instance whose
// provider schema offers nowhere to write a marker - GitHub issue #341.
//
// The verdict itself is exactly what it always was, and deliberately so:
// StatusUntaggable, never stamped, never counted as eligible. Untaggability
// is not an identity problem (live/MARKERS.md, and HANDOFF's invariant): the
// association, attachment and membership family is admitted precisely
// because its identity re-derives from tagged parents or from its own
// declaration. What changes is that the run no longer throws away the one
// other thing a migration owes such an instance.
//
// # What was thrown away
//
// A residue attribute is an argument the provider's own Read never returns -
// aws_route53_record.allow_overwrite is the founding case, and
// projection/residue.go's doc comment already named it. Stock OpenTofu never
// notices, because the state file remembers what was sent; issue #73 deletes
// the state file, so the FIRST live-plan after a clean migrate sees it null
// and proposes an update that applying does not settle. Issue #327 closed
// that for stamped resources and could not reach an untaggable one, because
// the only carrier out of Ratify was the one taggability gates.
//
// # No ReadResource here, and that is not an optimisation either
//
// The object handed forward is the state's own, decoded against the
// provider's current schema - the same source [projection.WriteBack] takes an
// applied object from. Three reasons, in order of weight:
//
//  1. It is the RIGHT object. A residue attribute is by definition one no
//     read returns, so a fresh read cannot tell us anything about it that the
//     state does not already say, and the last apply's value is the value.
//  2. Reaching a read here would let an untaggable instance come back
//     MISSING or DRIFTED, moving live-import's reported counts for resources
//     this run was never going to write to.
//  3. A per-instance read for every untaggable instance is new live traffic
//     at migrate time. The classifier's own two reads still happen, at
//     Approve, and only for an instance that has a candidate argument at all.
//
// A store-less run (no live block, or a live block with no record_store)
// builds no carrier, so it behaves exactly as it did before this existed.
func ratifyUntaggable(entry Entry, provider providers.Interface, schema providers.Schema, typeName string, inst *states.ResourceInstance) (Entry, carriers) {
	entry.Status = StatusUntaggable
	entry.Detail = fmt.Sprintf("%s has no tags argument in the provider's schema, so there is nowhere on it to carry an ownership marker.", typeName)

	prior, decErr := inst.Current.Decode(schema.Block.ImpliedType())
	if decErr != nil {
		// Deliberately NOT StatusMissing: this instance was untaggable before
		// this run tried to decode anything, that verdict is already correct
		// and already reported, and a decode failure here only means no
		// residue can be classified. Downgrading the verdict would move
		// live-import's own UNTAGGABLE count for a reason that has nothing to
		// do with markers.
		entry.Detail += fmt.Sprintf(" Its recorded state does not fit the provider's current schema, so no argument values could be recorded for it either: %s.", decErr)
		return entry, carriers{}
	}
	applied, _ := prior.Value.UnmarkDeep()
	entry.LiveID = liveIDFrom(typeName, applied)

	return entry, carriers{residuable: &residuable{
		provider: provider,
		schema:   schema,
		typeName: typeName,
		applied:  applied,
		identity: prior.Identity,
		private:  prior.Private,
	}}
}

// recordBackedType is the one property this whole mechanism keys on, read
// from the same place [projection.WriteBack] reads it after an apply:
// [identity.TypeIdentity.RecordBacked], which tools/row-gen derives from
// live/logical-schemas.json's per-provider store_only gate. There is no list
// of type names here and there must never be one - a provider whose types
// row-gen newly classifies store_only is migrated by this path the moment
// the table regenerates, with nothing in Go to edit.
func recordBackedType(typeName string) bool {
	ti, ok := identity.LookupType(typeName)
	return ok && ti.RecordBacked
}

// ratifyRecordBacked is ratifyOne's verdict for a record-backed instance: the
// state's own object, decoded against the provider's current schema and
// carried forward for Approve to seed the record store with.
//
// No ReadResource call is made, and that is not an optimisation. There is
// nothing in the live system to read: the value a record-backed resource has
// was generated once, by the provider, and the state file is the only place
// it has ever existed. hashicorp/random's Read is a pure prior-passthrough,
// so a call would answer with exactly the object handed to it and prove
// nothing - and hashicorp/local's would answer about a file on whichever
// machine ran the migration. The state is the source of truth here in the
// way the live system is the source of truth for a stamped resource, which
// is the same split [projection.WriteBack] makes when it takes the final
// state's object rather than re-reading the provider.
func ratifyRecordBacked(entry Entry, typeName string, schema providers.Schema, inst *states.ResourceInstance) (Entry, carriers) {
	prior, decErr := inst.Current.Decode(schema.Block.ImpliedType())
	if decErr != nil {
		entry.Status = StatusMissing
		entry.Detail = fmt.Sprintf("The recorded state does not fit the provider's current schema for %s: %s.", typeName, decErr)
		return entry, carriers{}
	}
	// The MARKED object is what goes into the record store, and the unmark
	// here is only for this function's own reads.
	//
	// [states.ResourceInstanceObjectSrc.Decode] re-applies the state's
	// AttrSensitivePaths, so a record-backed instance with a sensitive
	// attribute arrives marked - hashicorp/local marks local_file.content
	// sensitive and local_file is RECORD_BACKED (issue #314), so this is the
	// ordinary case rather than an edge one. [projection.encodeRecordPayload]
	// now splits that value from its sensitivity and persists both, the way
	// a state file persists AttrSensitivePaths beside AttrsJSON; passing it
	// an already-unmarked value would silently drop the sensitivity and
	// leave the first live-plan after a migration proposing a
	// sensitivity-only in-place update forever.
	//
	// liveIDFrom reads attributes out of the object, so it takes the
	// unmarked copy: an identity component is a cloud tag in plaintext and
	// must never be composed from a marked value.
	val, _ := prior.Value.UnmarkDeep()

	entry.Status = StatusUntaggable
	entry.LiveID = liveIDFrom(typeName, val)
	entry.Detail = fmt.Sprintf("%s has no live object to tag: its value IS its identity, so an approved migration seeds this estate's record store from the state's own object for it rather than writing a marker.", typeName)
	return entry, carriers{recordable: &recordable{
		typeName: typeName,
		value:    prior.Value,
		private:  prior.Private,
		status:   prior.Status,
	}}
}

// impliedProviderAddr is the provider configuration this run reaches a
// resource through: the default provider implied by its type ("aws" for
// every "aws_*" type), in the root module, keeping whatever alias the state
// recorded. This is deliberately unconditional on which module res itself
// lives in - a child-module resource inheriting its caller's default
// provider (the common case, and the only case a module block with no
// explicit `providers = {...}` map can produce) resolves exactly the same
// way a root resource does.
//
// It is deliberately not res.ProviderConfig verbatim. A tfstate written by
// an older, unrelated tool - real Terraform, most realistically, which is
// exactly who this package exists to migrate from - records a provider
// address under registry.terraform.io; this run's own plugin cache, and the
// working directory's own "init", know only registry.opentofu.org. Every
// other live-* command resolves a resource's provider from its configuration
// for the same reason, never from a recorded address. A future alias- or
// namespace-aware config lookup could refine this further - a module given
// an explicit `providers = {...}` map that rebinds an alias resolves wrong
// here, same as a root resource on a non-default provider always has - but
// v1 does not need one: the default provider address is what "choudoufu
// init" in this directory already prepared.
func impliedProviderAddr(res *states.Resource) addrs.AbsProviderConfig {
	return addrs.AbsProviderConfig{
		Module:   addrs.RootModule,
		Provider: addrs.ImpliedProviderForUnqualifiedType(res.Addr.Resource.ImpliedProvider()),
		Alias:    res.ProviderConfig.Alias,
	}
}

// admittedByProviderSchema is the schema-based admission fallback
// internal/live/lint's admitted() already applies at plan/lint time
// (identity.SynthesizeTypeIdentity): a type identity.DefaultTable does not
// cover, but whose identity the provider's own schema settles completely
// from a single required argument, the same way row-gen itself proposes a
// [client-named] row. Without this, live-import refused to even read a
// type that a plain live-plan over the identical configuration admits fine
// - aws_s3_bucket_acl among six sibling aws_s3_bucket_* types, found
// crossing terraform-aws-modules/terraform-aws-s3-bucket's "complete"
// example, none of which have a static table row because the runtime
// schema fallback already covers them everywhere else in this fork.
//
// Fetching the provider or its schema failing here is not itself reported:
// it just means the fallback does not apply, and the caller's ordinary "no
// identity knowledge" refusal stands exactly as it did before this existed.
//
// The claim above - that this is the same admission lint applies - is a
// claim about ONE function, [identity.SynthesizeTypeIdentity], and it was
// briefly false. Issue #331's veto landed inside lint's own admitted() and
// not inside the fallback, so for a day this function admitted a type
// (aws_iam_policy_attachment) that a live-plan over the identical
// configuration refused: live-import migrated it out of a tfstate and
// reported success, and the next plan said it was not admitted. The veto now
// sits in the fallback itself ([identity.NotImportable]), so the equivalence
// this comment asserts is held by construction rather than by two functions
// being kept in step - which is what live/notimportable_routes_test.go
// measures over the whole provider surface.
func admittedByProviderSchema(ctx context.Context, req Request, res *states.Resource, typeName string) bool {
	provider, err := req.Providers.ConfiguredProvider(ctx, impliedProviderAddr(res))
	if err != nil {
		return false
	}
	resp := provider.GetProviderSchema(ctx)
	if resp.Diagnostics.HasErrors() {
		return false
	}
	_, ok := identity.SynthesizeTypeIdentity(typeName, resp.ResourceTypes, nil)
	return ok
}

func resourceSchema(ctx context.Context, provider providers.Interface, typeName string) (providers.Schema, error) {
	resp := provider.GetProviderSchema(ctx)
	if resp.Diagnostics.HasErrors() {
		return providers.Schema{}, fmt.Errorf("the provider's schema could not be read: %w", resp.Diagnostics.Err())
	}
	schema, ok := resp.ResourceTypes[typeName]
	if !ok || schema.Block == nil {
		return providers.Schema{}, fmt.Errorf("the provider offers no schema for resource type %q", typeName)
	}
	return schema, nil
}

// liveIDFrom is the identity of a decoded state object, following the
// identity table's IdentityAttrs for the type and falling back to "id" -
// the same lookup [github.com/intentius/choudoufu/internal/live/mv] uses.
func liveIDFrom(typeName string, obj cty.Value) string {
	if obj == cty.NilVal || obj.IsNull() || !obj.Type().IsObjectType() {
		return ""
	}
	attrs := []string{"id"}
	if ti, ok := identity.LookupType(typeName); ok && len(ti.IdentityAttrs) > 0 {
		attrs = ti.IdentityAttrs
	}
	for _, name := range attrs {
		if !obj.Type().HasAttribute(name) {
			continue
		}
		v, _ := obj.GetAttr(name).Unmark()
		if v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
			continue
		}
		if s := v.AsString(); s != "" {
			return s
		}
	}
	return ""
}

// ---------------------------------------------------------------------------
// Deterministic walk order
// ---------------------------------------------------------------------------

func sortedModules(state *states.State) []*states.Module {
	mods := make([]*states.Module, 0, len(state.Modules))
	for _, m := range state.Modules {
		mods = append(mods, m)
	}
	sort.Slice(mods, func(i, j int) bool { return mods[i].Addr.String() < mods[j].Addr.String() })
	return mods
}

func sortedResources(mod *states.Module) []*states.Resource {
	out := make([]*states.Resource, 0, len(mod.Resources))
	for _, r := range mod.Resources {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.String() < out[j].Addr.String() })
	return out
}

func sortedInstanceKeys(res *states.Resource) []addrs.InstanceKey {
	keys := make([]addrs.InstanceKey, 0, len(res.Instances))
	for k := range res.Instances {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return res.Addr.Instance(keys[i]).String() < res.Addr.Instance(keys[j]).String()
	})
	return keys
}

func joinList(items []string) string {
	out := ""
	for i, s := range items {
		if i > 0 {
			out += ", "
		}
		out += s
	}
	return out
}
