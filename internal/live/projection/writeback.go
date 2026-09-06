// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/intentius/choudoufu/internal/tofu"
)

// WriteBackRequest is what [WriteBack] needs: the store a projection was
// built against, the versions it read at plan time, and the state an apply
// finished with.
type WriteBackRequest struct {
	// Store is GitHub issue #364's one per-instance record store, carrying
	// both the kind=object half (record-backed instances, issue #73) and
	// the kind=identity half (record-located identities issue #270,
	// residue issue #275, provisioner taint issue #353). A nil Store makes
	// [WriteBack] a no-op: no live block configured one, so there is
	// nothing to write back to.
	Store *RecordStore

	// PriorVersions is the projection's own record of what it read at plan
	// time - [Result.RecordVersions] - for every kind=object (record-backed)
	// instance. An address with no entry here had no prior record.
	PriorVersions []RecordVersion

	// EnvelopeVersions is [Result.EnvelopeVersions]: the plan-time version
	// of every kind=identity envelope that already existed, covering the
	// located, residue and provisioned concerns together - see that field's
	// own comment for why one list is correct now that the three share one
	// physical key.
	EnvelopeVersions []RecordVersion

	// FinalState is the state an apply (or destroy) finished with.
	FinalState *states.State

	// Schemas resolves a resource type's current schema, needed to decode
	// FinalState's stored objects before they can be re-encoded as a
	// record payload.
	Schemas *tofu.Schemas

	// Providers is how the residue half reaches a configured provider to
	// classify with. It is needed because there is NO static answer to
	// which arguments a provider's Read manages - issue #275 measured the
	// whole of hashicorp/aws 6.59.0 looking for one - so the only way to
	// find out is to ask the provider, twice, and compare. See
	// [classifyResidue].
	//
	// Nil skips the residue half entirely, with a warning naming what was
	// skipped rather than silently: an estate that declared a record_store
	// and then never got a residue record written would show the perpetual
	// diff forever with nothing saying why.
	//
	// The instances it hands back must be configured, exactly as
	// [Providers] requires for the plan side. A run that closed its
	// plan-time providers - which internal/command's stateless runner does,
	// deliberately, before the plan graph starts - has to open new ones for
	// this.
	Providers Providers

	// Config is the configuration this run planned and applied. The
	// located and provisioned halves both need it: whether an instance
	// gets a located identity turns on the `markers "record"` selection,
	// and whether it gets a taint record turns on whether its resource
	// block declares a create-time provisioner - both facts about the
	// configuration, neither recoverable from the final state.
	//
	// Nil disables the located and provisioned halves' write sides
	// entirely, which is the correct fail-safe: with no configuration to
	// ask, no instance can be proven selected or proven to declare a
	// provisioner, so nothing is written for either. The delete side still
	// runs for an address the final state no longer has at all, because
	// that walks the versions this run's plan read rather than the
	// configuration.
	Config *configs.Config

	// RootOutputStore is where GitHub issue #349's remaining half persists:
	// the value each root-level `output` block settled on, so the next
	// stateless plan has the "before" side stock reads out of its state
	// file. Nil makes that half of [WriteBack] a no-op, the same way a nil
	// Store does for the record-backed half. See rootoutput.go.
	//
	// It needs no *Versions companion. The other two halves read their
	// keys at plan time and write conditionally on what they read, because
	// what they hold is an identity and losing a race on one would put a
	// stale identity in front of the next run. This one reads its expected
	// version immediately before the write instead - see
	// [WriteRootOutputValues] - because losing the race is the correct
	// outcome here: the winner wrote a value from a state at least as new
	// as this one.
	RootOutputStore *RootOutputStore
}

// WriteBack persists every managed instance's post-apply record to
// req.Store: the kind=object half for GitHub issue #73's record-backed
// resources, and the kind=identity half - GitHub issues #270, #275 and #353
// - for every other instance whose located identity, residue or provisioner
// taint this run's apply settled. It deletes the record of anything the
// final state no longer has at all, called once, after a successful apply
// (or destroy), never mid-apply.
//
// Every write and delete is conditional on the version [Result] read at
// plan time (staterecord.Store.PutIfVersion / Delete), so a second writer
// that changed the same record between this run's plan and apply produces a
// *staterecord.VersionConflictError, which this function turns into a loud,
// run-stopping error diagnostic naming both versions - the same philosophy
// live/MARKERS.md's marker-collision handling already uses, never a silent
// last-writer-wins. A conflict on one instance does not stop this function
// from attempting every other instance: an operator fixing one conflict at
// a time should see every conflict in the estate, not just the first one
// alphabetically.
func WriteBack(ctx context.Context, req WriteBackRequest) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	diags = diags.Append(writeBackRecordEnvelopes(ctx, req))
	// Issue #349's root output values. Deliberately outside the req.Store
	// nil-check below: this namespace has its own store handle and an
	// estate can perfectly well have one without a record-backed resource
	// in it. It raises nothing - see [WriteRootOutputValues].
	writeBackRootOutputs(ctx, req)

	if req.Store == nil {
		return diags
	}

	seen := make(map[string]bool, len(req.PriorVersions))

	if req.FinalState != nil {
		for _, entry := range req.FinalState.AllResourceInstanceObjectAddrs() {
			if entry.DeposedKey != states.NotDeposed {
				// A deposed object is a mid-replace leftover the graph is
				// about to discard; the record for this address is the
				// current object's job, materialized in the next iteration
				// (or, for a purely-deposed address with no current object
				// at all, by nothing - which is correct, since that shape
				// means the replace's create side failed and there is
				// nothing new to persist).
				continue
			}
			addr := entry.Instance
			typeName := addr.Resource.Resource.Type

			ti, ok := identity.LookupType(typeName)
			if !ok || !ti.RecordBacked {
				continue
			}
			seen[addr.String()] = true

			res := req.FinalState.Resource(addr.ContainingResource())
			if res == nil {
				continue
			}
			ri := res.Instance(addr.Resource.Key)
			if ri == nil || ri.Current == nil {
				continue
			}

			schema, _ := req.Schemas.ResourceTypeConfig(res.ProviderConfig.Provider, addrs.ManagedResourceMode, typeName)
			if schema == nil || schema.Block == nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot persist a record",
					fmt.Sprintf("Writing the persisted record for %s failed: no schema is available for %s to decode its final state with.", addr, typeName),
				))
				continue
			}
			obj, err := ri.Current.Decode(schema.Block.ImpliedType())
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot persist a record",
					fmt.Sprintf("Writing the persisted record for %s failed: its final state could not be decoded: %s.", addr, err),
				))
				continue
			}

			of, err := encodeObjectFields(obj.Value, obj.Private, obj.Status)
			if err != nil {
				diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot persist a record",
					fmt.Sprintf("Writing the persisted record for %s failed: %s.", addr, err),
				))
				continue
			}

			expected := priorVersion(req.PriorVersions, addr)
			if _, err := req.Store.mergeEnvelope(ctx, addr, expected, func(env *recordEnvelope) {
				env.Kind = recordKindObject
				env.Object = of
				env.Provider = providerString(res.ProviderConfig)
				diffDeposedForWrite(env, ri, schema, typeName, res.ProviderConfig)
			}); err != nil {
				diags = diags.Append(writeBackConflictDiag(addr, "Writing", err))
			}
		}
	}

	for _, rv := range req.PriorVersions {
		if seen[rv.Addr.String()] {
			continue
		}
		// tombstone, not delete: this address left the final state
		// entirely, and if its record named an identity, that identity's
		// own tags can stay visible via the tagging API for a time after
		// the object is actually gone (maintainer ruling 2026-08-25,
		// corpus-ec2-instance-complete's day2_remove unit) - see
		// [RecordStore.tombstone] and [tombstoneFields]. A record with no
		// identity to carry forward (this loop's ordinary case: a
		// record-backed instance's identity concept lives in its Object
		// member, which tombstone never carries forward) reduces to
		// exactly the delete this replaced.
		if err := req.Store.tombstone(ctx, rv.Addr, rv.Version); err != nil {
			diags = diags.Append(writeBackConflictDiag(rv.Addr, "Deleting", err))
		}
	}

	return diags
}

// diffDeposedForWrite is GitHub issue #361's crash-window recovery,
// mirrored into whichever of [WriteBack]'s two loops already owns addr's
// envelope this pass - the record-backed (kind=object) loop above, or
// writeBackRecordEnvelopes's kind=identity loop below - by reading
// ri.Deposed, the map [states.ResourceInstance] already carries beside
// .Current, and diffing it against env.Deposed inside the SAME mutate
// closure the caller already passed to [RecordStore.mergeEnvelope]. See
// #361's design comment (issuecomment-5405599939), section 2.
//
//   - A key present in ri.Deposed now but absent from env.Deposed is new
//     this pass: its identity is rendered with [LocatedRecordFrom], the
//     same function every current-object writer in this file already
//     uses, so this reaches every recordable type generically rather than
//     naming one. A type or object [LocatedRecordFrom] cannot render an
//     identity for still gets an entry - Provider alone, per
//     [deposedFields.Provider]'s own doc comment ("a deposed object's
//     managing provider configuration is recoverable later") - because the
//     alternative, recording nothing, would let this same deposed object
//     look brand new on every later apply rather than merely
//     unidentified.
//   - A key present in env.Deposed but absent from ri.Deposed this pass is
//     gone: destroyed by this same apply's own crash-window recovery (the
//     ordinary, happy-path close of the window), or by a human working
//     around the estate. Either way it is deleted from the map, never left
//     to linger as a stale claimant discovery.go's collision-breaking
//     branch could later mismatch against a different live object that
//     happens to reuse the same identity.
//
// changed reports whether env.Deposed differs from what it held on entry -
// the signal writeBackRecordEnvelopes's own "touched" gate needs, since
// unlike identity/residue/taint, a deposed-only change can be the SOLE
// reason an address needs a write this pass.
func diffDeposedForWrite(env *recordEnvelope, ri *states.ResourceInstance, schema *providers.Schema, typeName string, providerAddr addrs.AbsProviderConfig) (changed bool) {
	if ri == nil {
		return false
	}

	for dk := range env.Deposed {
		if _, stillDeposed := ri.Deposed[states.DeposedKey(dk)]; !stillDeposed {
			changed = true
			break
		}
	}
	var additions map[string]*deposedFields
	for dk, obj := range ri.Deposed {
		key := string(dk)
		if _, already := env.Deposed[key]; already {
			continue
		}
		changed = true
		df := &deposedFields{Provider: providerString(providerAddr)}
		if obj != nil && schema != nil && schema.Block != nil {
			if decoded, err := obj.Decode(schema.Block.ImpliedType()); err == nil {
				if rec, ok := LocatedRecordFrom(typeName, *schema, decoded.Value); ok {
					df.Identity = &identityPayload{ImportID: rec.ImportID, Attrs: rec.Components}
				}
			}
		}
		if additions == nil {
			additions = make(map[string]*deposedFields, len(ri.Deposed))
		}
		additions[key] = df
	}

	if !changed {
		return false
	}

	next := make(map[string]*deposedFields, len(env.Deposed)+len(additions))
	for dk, df := range env.Deposed {
		if _, stillDeposed := ri.Deposed[states.DeposedKey(dk)]; stillDeposed {
			next[dk] = df
		}
	}
	for dk, df := range additions {
		next[dk] = df
	}
	if len(next) == 0 {
		next = nil
	}
	env.Deposed = next
	return true
}

// deposedRecordedDiffers reports whether addr's currently recorded Deposed
// key set differs from live, the cheap pre-check writeBackRecordEnvelopes
// uses to decide whether an address needs a write for [diffDeposedForWrite]
// alone. It compares key presence only - not rendered content - which is
// enough to decide "does a write need to happen", the only question this
// answers; the write itself (and the identity it renders for a genuinely
// new key) happens once, inside mergeEnvelope's own mutate closure, against
// whatever that call's own fresh read finds.
func deposedRecordedDiffers(ctx context.Context, store *RecordStore, addr addrs.AbsResourceInstance, live map[states.DeposedKey]*states.ResourceInstanceObjectSrc) bool {
	recorded, _, _, err := store.GetDeposed(ctx, addr)
	if err != nil {
		// Unreadable is not "unchanged": treated as a difference so the
		// mutate closure's own fresh read gets a chance to either recover
		// or raise the same error again through mergeEnvelope's ordinary
		// path, rather than this cheap pre-check silently swallowing it.
		return true
	}
	if len(recorded) != len(live) {
		return true
	}
	for dk := range live {
		if _, ok := recorded[string(dk)]; !ok {
			return true
		}
	}
	return false
}

// writeBackRecordEnvelopes is [WriteBack]'s kind=identity half: GitHub
// issue #364's merge of what used to be three independent write-back
// passes (writeBackLocated for issue #270, writeBackResidue for issue #275,
// writeBackProvisioned for issue #353) into one loop over the final state,
// because the three concerns now share one physical key per instance.
//
// # Why one loop and not three sequential ones
//
// Three sequential passes each doing their own read-modify-conditional-write
// against the SAME key would have the second and third pass's conditional
// write compare against a plan-time version the first pass has already
// moved past, failing with a spurious conflict on every instance more than
// one of the three concerns touches. One loop computes all three concerns
// for an address before writing, so there is exactly one
// [RecordStore.mergeEnvelope] call per address, and every concern's
// contribution rides the same conditional write.
//
// # What each concern does when it does not apply this pass
//
// This is where the fidelity to the three predecessor functions' own rules
// lives, and each rule is a deliberate, independent choice - see
// writeBackLocated, writeBackResidue and writeBackProvisioned in this
// package's git history for the shape each one is reproducing:
//
//   - Located identity: wanted (the type is automatically located, or the
//     `markers "record"` selection covers this address) and derivable -> SET
//     it, overwriting whatever was there. Wanted but not derivable (the
//     final state could not be decoded, or the applied object carries no
//     usable identity) -> an ERROR, and the existing identity (if any) is
//     left exactly alone. Not wanted (the type is not located and the
//     selection does not cover this address, or no schema could be found at
//     all) -> CLEAR it, matching writeBackLocated's own "not seen this pass"
//     rule, which reaches the trailing deletion loop for the SAME outcome
//     when the two lived in separate namespaces.
//   - Residue: whenever the final object decodes, every current candidate
//     is reclassified. Classification succeeding -> SET the fresh residue,
//     replacing whatever was recorded before even if it now classifies a
//     different set of attributes. Nothing to classify, or classification
//     proving nothing -> LEAVE ALONE: an address still present whose
//     classification failed this time keeps its previous record, which is
//     self-healing on the next successful classification. Decode failing
//     entirely -> CLEAR, matching writeBackResidue's own "not seen" rule.
//   - Provisioner taint: the resource block declares a create-time
//     provisioner and the final object came out tainted -> SET the taint.
//     Declares one and came out healthy -> CLEAR a stale taint. Declares
//     none -> LEAVE ALONE, exactly as before: the key sits inert until the
//     block comes back.
//
// An address none of the three concerns engages with this pass is left out
// of the merge entirely - not even a no-op write - so it neither appears in
// `seen` nor forces a version bump; whatever was recorded for it earlier
// stays exactly as it was, word for word what writeBackLocated,
// writeBackResidue and writeBackProvisioned already guaranteed on their own
// namespaces.
//
// # The stale-key fallback
//
// [RecordStore.currentVersion] backstops every merge whose plan-time
// EnvelopeVersions has no entry for the address: a plan that never read
// this key (the object was reported absent, or the resource block was not
// yet in the configuration) has nothing to assert a race against, but a
// PREVIOUS incarnation of the same address may have left a stale envelope
// behind - TestTaintIsRecordedOverAStaleRecordFromAnEarlierIncarnation is
// the shape that first needed this for the provisioned half alone; sharing
// one key now makes it load-bearing for the located and residue halves too.
func writeBackRecordEnvelopes(ctx context.Context, req WriteBackRequest) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if req.Store == nil {
		return diags
	}

	selection := identity.SelectionFor(req.Config)
	secrets := identity.SecretsFor(req.Config)
	providerCache := map[string]providers.Interface{}

	// noProvidersWarned makes the "no provider access to classify residue
	// with" warning fire once per write-back rather than once per instance
	// that has a residue candidate - the same one-warning-per-run shape the
	// pre-#364 writeBackResidue gave a nil req.Providers.
	noProvidersWarned := false

	seen := make(map[string]bool, len(req.EnvelopeVersions))

	// unrecordableLogged makes issue #671's skip visible once per TYPE per
	// write-back: a silently absent record is indistinguishable from a
	// forgotten one, and the read half (superseded-claimant recovery, the
	// envelope-vouching arm) now does real work from records.
	//
	// The line it gates is one of TWO, split by GitHub issue #746's review
	// finding B4: this branch is reached both when the identity is one this
	// mechanism structurally cannot derive and when recording it is refused
	// on purpose because it would put secret material in the record store,
	// and printing one line for both is exactly the blur the branch's own
	// comment says must never happen. [identity.SensitiveIdentityAttr] is
	// which of the two, asked once per instance that reaches the branch.
	unrecordableLogged := map[string]bool{}

	if req.FinalState != nil {
		for _, entry := range req.FinalState.AllResourceInstanceObjectAddrs() {
			if entry.DeposedKey != states.NotDeposed {
				continue
			}
			addr := entry.Instance
			typeName := addr.Resource.Resource.Type
			if ti, ok := identity.LookupType(typeName); ok && ti.RecordBacked {
				// A record-backed instance's whole record is the kind=object
				// loop's job; there is nothing here for it.
				continue
			}

			res := req.FinalState.Resource(addr.ContainingResource())
			if res == nil {
				continue
			}
			ri := res.Instance(addr.Resource.Key)
			if ri == nil || ri.Current == nil {
				continue
			}

			var (
				touched                                bool
				clearIdentity, clearResidue, clearProv bool
				setIdentity                            *identityPayload
				setResidue                             *residueFields
				setProv                                *provisionedFields
			)

			schemaPtr, _ := req.Schemas.ResourceTypeConfig(res.ProviderConfig.Provider, addrs.ManagedResourceMode, typeName)

			// ---- identity (issue #270's located route, and - since GitHub
			// issue #364 unit A2's foundation-order ruling - every other
			// instance too) ----
			//
			// automatic and selected instances are unchanged from before
			// this issue: a located route's record is such an instance's
			// ONLY way to be found again, so a derivation failure there
			// stays the loud error it always was. Every other (ordinary
			// taggable) instance now ALSO gets its identity recorded, best
			// effort: ownership is decided by its marker regardless, so a
			// type or instance this pass cannot derive an identity for
			// (an unrecordable schema, an object missing a component) just
			// keeps whatever was already recorded - from an earlier apply,
			// or from a live-import migration - rather than failing the
			// apply or erasing it. See writeBackRecordEnvelopes's own
			// "residue" case just below for the same leave-alone shape.
			switch {
			case schemaPtr == nil || schemaPtr.Block == nil:
				clearIdentity = true
			default:
				schema := *schemaPtr
				typeSchemas := map[string]providers.Schema{typeName: schema}
				automatic := identity.LocatedType(typeName, typeSchemas)
				selected := selection.Selects(addr.ConfigResource()) && identity.SelectedLocatedType(typeName, typeSchemas)

				obj, err := ri.Current.Decode(schema.Block.ImpliedType())
				switch {
				case err != nil && (automatic || selected):
					touched = true
					diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot record a located identity",
						fmt.Sprintf("Recording which live %s %s owns failed: its final state could not be decoded: %s.", typeName, addr, err),
					))
				case err != nil:
					// An ordinary instance this pass could not decode for
					// identity purposes: leave whatever is recorded alone.
					// The marker (or the record-backed loop, for a
					// record-backed type) is this instance's real
					// ownership carrier regardless.
				default:
					rec, recordable := LocatedRecordFrom(typeName, schema, obj.Value)
					switch {
					case recordable:
						touched = true
						setIdentity = &identityPayload{ImportID: rec.ImportID, Attrs: rec.Components}
					case automatic || selected:
						touched = true
						diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot record a located identity",
							fmt.Sprintf(
								"Recording which live %s %s owns failed: the applied object carries no usable identity to record. A %s carries no ownership marker, so without this record no later run can find the object again, and the next plan would propose creating a second one.",
								typeName, addr, typeName,
							),
						))
					default:
						// Not recordable, and not this instance's only
						// carrier: leave whatever is recorded alone rather
						// than clearing it - a value written by an earlier
						// apply or a live-import migration must not be
						// erased just because THIS pass could not re-derive
						// it.
						//
						// Two different things reach here and issue #671
						// said they must not look the same: an identity
						// this mechanism structurally cannot hold in full,
						// and the deliberate refusal to write a sensitive
						// attribute into the record store. Until GitHub
						// issue #746's finding B4 they printed the
						// identical line, which is that distinction blurred
						// in the one place it was supposed to be visible.
						// They are now two messages, each naming what an
						// operator would do about it - nothing, for the
						// first; nothing either, for the second, but for a
						// reason they chose.
						//
						// Both stay log lines rather than becoming plan
						// diagnostics. Nothing is lost in this branch: it
						// is by construction the case where the record is
						// NOT this instance's only identity carrier (the
						// automatic and selected arms above raise a real
						// error), so the marker still decides ownership and
						// a per-apply warning would be about a redundant
						// carrier.
						//
						// #746 left open whether the UNTAGGABLE slice of
						// this population - the one where the record really
						// is the only surviving identity carrier, marker or
						// no marker - deserves more than a log line. Issue
						// #855 measured it: at hashicorp/aws 6.59.0
						// (2026-09-05), zero of the 27 markerless,
						// wire-identity-composite types with no list
						// resource, no Cloud Control enumeration source and
						// no content-match binding carry a sensitive
						// identity attribute
						// (identity.SensitiveIdentityAttr). The population
						// this branch's sensitive arm actually reaches, for
						// an instance with nowhere else to be found, is
						// empty today, so there is nothing to promote.
						// internal/live/identity's
						// TestUntaggableUnlistableSensitivePopulation pins
						// the 27 and the (empty) sensitive subset by value;
						// a failure there naming an added type is what
						// reopens this question.
						if !unrecordableLogged[typeName] {
							unrecordableLogged[typeName] = true
							if secret := identity.SensitiveIdentityAttr(typeName, schema); secret != "" {
								log.Printf("[INFO] projection: no identity record is written for %s (e.g. %s) on purpose: the identity a record would hold is its %q attribute, which the provider marks sensitive, and the record store is not where this fork puts secret material. Ownership stays marker-carried and any existing record is left alone", typeName, addr, secret)
							} else {
								log.Printf("[INFO] projection: no identity record can be derived for %s (e.g. %s): this mechanism cannot hold its identity in full. Ownership stays marker-carried and any existing record is left alone", typeName, addr)
							}
						}
					}
				}
			}

			// ---- residue (issue #275) ----
			if schemaPtr != nil && schemaPtr.Block != nil {
				schema := *schemaPtr
				obj, err := ri.Current.Decode(schema.Block.ImpliedType())
				if err != nil {
					clearResidue = true
				} else {
					touched = true
					candidates := residueCandidates(schema, obj.Value, secrets)
					pathCandidates := residueLeafPathCandidates(schema, obj.Value, secrets)
					if len(candidates)+len(pathCandidates) > 0 && req.Providers == nil {
						if !noProvidersWarned {
							noProvidersWarned = true
							diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryResidueNotClassified,
								"This estate declares a record_store, but the apply had no provider access to classify with, so no argument values were recorded. Arguments the provider's read does not return will be proposed for update again on the next plan. Nothing was changed and nothing was written.",
							))
						}
						// Leave residue exactly as it was: neither set nor
						// cleared. A nil req.Providers is a caller that never
						// intended to classify this run, not a proof that
						// this instance has no residue.
					} else if len(candidates)+len(pathCandidates) > 0 {
						applied, _ := obj.Value.UnmarkDeep()
						provider, provErr := residueProvider(ctx, req.Providers, providerCache, res.ProviderConfig)
						if provErr != nil {
							diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryResidueNotClassified, fmt.Sprintf(
								"No argument values were recorded for %s: provider %s could not be reached to classify them (%s). Arguments the provider's read does not return will be proposed for update again on the next plan. Nothing was changed and nothing was written.",
								addr, res.ProviderConfig, provErr,
							)))
						} else {
							attrs, ok := classifyResidueAll(schema, applied, secrets, func(prior cty.Value) (cty.Value, error) {
								resp := provider.ReadResource(ctx, providers.ReadResourceRequest{
									TypeName:      typeName,
									PriorState:    prior,
									Private:       obj.Private,
									ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
									PriorIdentity: obj.Identity,
								})
								if resp.Diagnostics.HasErrors() {
									return cty.NilVal, resp.Diagnostics.Err()
								}
								return resp.NewState, nil
							}, obj.Identity)
							if ok {
								rf, encErr := encodeResidueFields(attrs)
								if encErr != nil {
									diags = diags.Append(tfdiags.Sourceless(tfdiags.Warning, SummaryResidueNotClassified, fmt.Sprintf(
										"The argument values classified for %s could not be recorded: %s. Those arguments will be proposed for update again on the next plan. Nothing in the live system was changed.",
										addr, encErr,
									)))
								} else {
									setResidue = rf
								}
							}
						}
					}
				}
			} else {
				clearResidue = true
			}

			// ---- provisioner taint (issue #353) ----
			if declaresCreateProvisioners(req.Config, addr) {
				touched = true
				if ri.Current.Status == states.ObjectTainted {
					setProv = &provisionedFields{Tainted: true}
				} else {
					clearProv = true
				}
			}

			// ---- deposed objects (issue #361) ----
			//
			// Unlike identity/residue/taint above, a deposed-only change
			// can be the SOLE reason this address needs a write this pass
			// - the crash-window recovery closes exactly when a stale
			// Deposed entry needs clearing and nothing else about the
			// address changed. touched is decided from a cheap key-set
			// comparison against what is already recorded (an extra read,
			// the same "stale-key fallback" trade this function's own doc
			// comment already makes below); the real diff, including
			// rendering a new entry's identity, runs once inside the
			// mutate closure against whatever mergeEnvelope's own fresh
			// read finds - see [diffDeposedForWrite].
			if false && !touched && deposedRecordedDiffers(ctx, req.Store, addr, ri.Deposed) {
				touched = true
			}

			if !touched {
				continue
			}
			seen[addr.String()] = true

			expected := priorVersion(req.EnvelopeVersions, addr)
			if expected == "" {
				// See this function's own "stale-key fallback" doc.
				if v, err := req.Store.currentVersion(ctx, addr); err == nil {
					expected = v
				}
			}

			_, err := req.Store.mergeEnvelope(ctx, addr, expected, func(env *recordEnvelope) {
				// GitHub issue #670, before env.Provider is overwritten
				// and before the identity is: a replace leaves this
				// address in the final state wearing a DIFFERENT object,
				// so the delete-side tombstone loop below never sees it,
				// and without an entry here the object this apply
				// destroyed is merely unrecorded rather than provably
				// dead. See [supersedeIdentity] for why the identity
				// changing is the evidence, and for why a wrong entry
				// costs a refusal and nothing else.
				//
				// clearIdentity is deliberately not a supersession: it
				// means this pass could not ask the schema whether the
				// type carries a recordable identity at all, which is a
				// fact about this run's provider access, not about the
				// object.
				if setIdentity != nil {
					supersedeIdentity(env, setIdentity)
				}
				env.Provider = providerString(res.ProviderConfig)
				switch {
				case setIdentity != nil:
					env.Identity = setIdentity
				case clearIdentity:
					env.Identity = nil
				}
				switch {
				case setResidue != nil:
					env.Residue = setResidue
				case clearResidue:
					env.Residue = nil
				}
				switch {
				case setProv != nil:
					env.Provisioned = setProv
				case clearProv:
					env.Provisioned = nil
				}
				diffDeposedForWrite(env, ri, schemaPtr, typeName, res.ProviderConfig)
			})
			if err != nil {
				diags = diags.Append(writeBackConflictDiag(addr, "Writing", err))
			}
		}
	}

	for _, rv := range req.EnvelopeVersions {
		if seen[rv.Addr.String()] {
			continue
		}
		// tombstone, not delete: see the identical comment on the
		// record-backed loop's own version of this cleanup, above. This
		// is the loop that actually populates env.Identity for an
		// ordinary taggable or located instance (the "issue #364 unit A2"
		// identity write, above), so this is where day2_remove's own
		// stale-tag-after-destroy collision is actually closed.
		if err := req.Store.tombstone(ctx, rv.Addr, rv.Version); err != nil {
			diags = diags.Append(writeBackConflictDiag(rv.Addr, "Deleting", err))
		}
	}

	return diags
}

// priorVersion looks up addr's plan-time version in versions, "" (the
// store's own create/absence convention) when there is no entry.
func priorVersion(versions []RecordVersion, addr addrs.AbsResourceInstance) string {
	want := addr.String()
	for _, rv := range versions {
		if rv.Addr.String() == want {
			return rv.Version
		}
	}
	return ""
}

// writeBackConflictDiag turns a write-back failure into a run-stopping
// error diagnostic, naming both versions when it is a
// *staterecord.VersionConflictError so an operator sees exactly what
// changed underneath this run rather than a bare "conflict" - the same
// naming-both-sides discipline live/MARKERS.md's marker-collision handling
// already uses. verb is "Writing" or "Deleting", matching the operation
// that failed.
func writeBackConflictDiag(addr addrs.AbsResourceInstance, verb string, err error) tfdiags.Diagnostics {
	var vErr *staterecord.VersionConflictError
	if errors.As(err, &vErr) {
		return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "Record store write conflict", fmt.Sprintf(
			"%s the persisted record for %s failed: another writer changed it between this run's plan and apply. This run expected version %q and the store now holds version %q. Nothing was overwritten. Re-plan and re-apply to reconcile with the other writer's change.",
			verb, addr, displayVersion(vErr.ExpectedVersion), displayVersion(vErr.ActualVersion),
		)))
	}
	return tfdiags.Diagnostics{}.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot persist a record", fmt.Sprintf(
		"%s the persisted record for %s failed: %s.", verb, addr, err,
	)))
}

// displayVersion renders staterecord's "" (no record) sentinel as an
// operator-facing word rather than an empty, easy-to-miss string.
func displayVersion(v string) string {
	if v == "" {
		return "(no record)"
	}
	return v
}
