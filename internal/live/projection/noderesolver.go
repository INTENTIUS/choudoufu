// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package projection

import (
	"context"
	"fmt"

	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/strict"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// NodeResolver is the live path's implementation of GitHub issue #388's
// plan-node seam (internal/tofu.ResourceIdentityResolver;
// rfc/20260823-foundation-order-ruling.md, ruling 3). It answers "does
// this instance already have an identity" from three sources, in the order
// HANDOFF.md's foundation section fixes: the estate's record
// ([Ownership] #364's write half), the marker sweep this run already
// performed (#364's read half, computed pre-walk today), and the identity
// table applied to the instance's real, evaluated configuration value
// (ruling 3's own contribution - see [identity.ComponentsFromValue]).
//
// It is deliberately built to satisfy internal/tofu.ResourceIdentityResolver
// STRUCTURALLY - nothing here imports internal/tofu, and nothing in that
// package imports this one. See resource_identity.go's own doc comment for
// why: the dependency has to run one way only, from the fork's live-mode
// package (which builds a *NodeResolver and hands it, as the interface, to
// tofu.ContextOpts) into the engine, never back.
//
// A zero-value *NodeResolver answers "not found" for everything and is
// therefore usable, if pointless, on its own - useful for a construction
// site that fills in RecordStore and MarkerIndex a step later than it
// creates the resolver (see internal/command/live_mode.go: the resolver has
// to exist BEFORE tofu.NewContext is called, because ContextOpts is where
// it is handed over, but its two data sources only exist after PriorState's
// record-store-open and marker-sweep steps have run - the same "build
// early, populate once the run knows more" shape statelessRunner itself
// already uses for recordStore).
type NodeResolver struct {
	// RecordStore is the estate's per-instance record ([RecordStore]),
	// exactly the store [builder.materializeFromRecord] reads from for the
	// pre-walk projection. Nil for an estate with no record_store block
	// (impliedRecordStore is never nil in that shape - #364's write half
	// still applies - so nil here means "this run never opened one," not
	// "this estate has no records").
	RecordStore *RecordStore

	// MarkerIndex is the discovery sweep's resolutions, keyed by
	// [addrs.AbsResourceInstance.String], for every instance the sweep
	// resolved a concrete import identity for. It is a snapshot: built once,
	// after PriorState's own marker sweep runs, from the same
	// []identity.Resolution the pre-walk projection consumes
	// (disco.Resolutions). A resolution with an empty ImportID and no
	// Identity object is skipped when the index is built - see
	// [NewMarkerIndex] - so every entry here is immediately usable.
	MarkerIndex map[string]providers.ImportTarget

	// NoSourceCreate is GitHub issue #365's ruling-4 toggle, read once and
	// carried here rather than re-read per call. It governs exactly one
	// shape: a CONFIG-IDENTIFIED type (a table row that is neither
	// ServerAssigned nor RecordBacked, so its identity is ordinarily
	// derivable from configuration) whose derivation nonetheless failed
	// for this specific instance, with no record and no marker either. The
	// DEFAULT (false) refuses that shape, because this run cannot tell
	// "genuinely new" apart from "real, and this run simply cannot derive
	// its identity yet," and creating a duplicate of a real object is
	// exactly the failure HANDOFF's safety rule exists to prevent. Set
	// true, it reports found=false with no diagnostic instead, letting
	// managedResourceExecute fall through to stock's own behavior: plan a
	// create.
	//
	// It does NOT govern a server-assigned type (an EC2 instance, a VPC:
	// minted at create time, with no configuration argument to derive
	// from) or a type absent from the table altogether - those have no
	// source to be missing in the first place, so a brand-new instance of
	// one always reports found=false with no diagnostic, whatever this
	// field is set to. Getting that boundary wrong once refused every
	// greenfield resource in an estate with no history at all; see
	// ResolveResourceIdentity's own comment and this file's tests.
	NoSourceCreate bool

	// Estate and Selection are GitHub issue #388's stamp half
	// ([NodeResolver.AdjustConfigValue] in nodestamp.go, populated at the
	// same "build early, populate once the run knows more" step as the
	// three fields above - see internal/command/live_mode.go and
	// live_plan.go, right where they set RecordStore/MarkerIndex/
	// NoSourceCreate). See nodestamp.go's own doc comment for what each is
	// for; they live here, not in a separate type, so one object serves
	// both tofu.ResourceIdentityResolver and tofu.ConfigValueAdjuster and
	// the two seams never drift onto two different snapshots of the same
	// run's estate name or selection.
	Estate    string
	Selection *strict.Selection

	// Slots is [discovery.Result.SlotTable]: the estate-wide sweep's slot
	// assignment, escaped instance address to slot value, the same map
	// stamp.Request.Slots already carries for the HCL path. Nil is a
	// completely ordinary value - a configuration with no count blocks
	// assigns no slots at all - and is read exactly like an absent map
	// entry: no tofu-slot tag written. See nodestamp.go's own doc comment.
	Slots map[string]string

	// Unowned names every declared instance [Ownership] (ownership.go)
	// refused to admit into the pre-walk projection's prior state, keyed
	// by [addrs.AbsResourceInstance.String] - the same set [Result.Unowned]
	// reports (audit finding C1's own fix: "a live object enters prior
	// state only if it carries this estate's marker"). It is NOT populated
	// alongside RecordStore/MarkerIndex/Estate above, because projection.
	// BuildWith - the call that actually decides ownership - runs a few
	// steps AFTER those are set in both internal/command/live_mode.go and
	// live_plan.go; see those files' own population sites, right after
	// their projDiags.HasErrors() check.
	//
	// Step (c) below is otherwise blind to ownership entirely: it derives
	// an import target from configuration alone, the exact shape C1's own
	// doc comment (ownership.go's [Ownership]) warns a client-named
	// resource is - "a configuration naming a bucket ... that already
	// exists and belongs to somebody else." The pre-walk projection was
	// fixed against that; without this field, step (c) reintroduces it at
	// the node for any type PriorState's own marker sweep does not cover
	// (config-identified types: aws_s3_bucket, not aws_vpc), because those
	// never reach [NodeResolver.MarkerIndex] either - nothing marks them
	// unowned there, and nothing here would have refused them. Caught by
	// TestLivePlan_unownedNameIsNotAdopted and
	// TestLivePlan_otherEstatesResourceIsNotAdopted once the flag defaulted
	// on and those tests' -estate form actually exercised this path for
	// the first time.
	Unowned map[string]bool
}

// NewMarkerIndex builds a [NodeResolver.MarkerIndex] from a discovery
// sweep's resolutions - the same slice ([]identity.Resolution) PriorState
// already merges into the pre-walk projection's input
// (disco.Resolutions/resolutions.All()). Only resolutions carrying a usable
// import identity (a non-empty ImportID, or a non-null Identity object) are
// kept; every other class (ClassParentDerived's Formula, ClassNeedsDiscovery
// with no match, ClassRecordBacked, ClassRecordLocated - which has no
// ImportID by design, see identity.ClassRecordLocated's own doc comment)
// has nothing this cheap, address-keyed lookup can serve, and
// [NodeResolver.ResolveResourceIdentity]'s other two steps (the record
// store, and the identity table over the evaluated value) are where those
// are served instead.
func NewMarkerIndex(resolutions []identity.Resolution) map[string]providers.ImportTarget {
	out := make(map[string]providers.ImportTarget, len(resolutions))
	for _, r := range resolutions {
		target := providers.ImportTarget{ID: r.ImportID, Identity: r.Identity}
		if !target.IsIDBased() && !target.IsIdentityBased() {
			continue
		}
		out[r.Addr.String()] = target
	}
	return out
}

// ResolveResourceIdentity implements internal/tofu.ResourceIdentityResolver.
//
// The three steps run in order and the first one to produce a usable
// [providers.ImportTarget] wins; none of them ever partially answer - a
// step that finds something malformed (a record whose identity fails the
// provider's own identity schema, say) falls through to the next step
// exactly as if it had found nothing, because a malformed answer and no
// answer both mean "this step has nothing to contribute," never "guess."
func (n *NodeResolver) ResolveResourceIdentity(ctx context.Context, addr addrs.AbsResourceInstance, config cty.Value, schema providers.Schema) (providers.ImportTarget, bool, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	// n.Unowned first, ahead of all three steps below, not only ahead of
	// step (c) - see [NodeResolver.Unowned]'s own doc comment for what
	// populates it and why. It has to gate (b) too: [NewMarkerIndex]'s
	// input (the merged resolutions PriorState hands this resolver) is
	// NOT restricted to genuine marker-sweep matches - it also carries
	// every ClassConcrete resolution the static evaluator produced
	// straight from configuration text, for a config-identified type like
	// aws_s3_bucket that discovery never touches at all. So (b) can
	// answer "found" from the exact same ownership-blind config-derived
	// guess (c) can, and TestLivePlan_otherEstatesResourceIsNotAdopted
	// caught it landing there first, before this check ever reached (c) -
	// gating only (c) planned an update onto a live object this run had
	// already, correctly, reported as belonging to another estate. (a) is
	// checked here too, defensively: RecordStore only ever holds THIS
	// estate's own prior writes, so a record and a fresh Unowned verdict
	// for the same address should not coexist in practice, but if a live
	// object's marker changed out from under a stale record, the marker -
	// what [Ownership] just read from the object itself, this run - is
	// what HANDOFF's foundation section names authoritative, not a record
	// that may now be stale.
	if n.Unowned[addr.String()] {
		return providers.ImportTarget{}, false, diags
	}

	// (a) The estate's record - GitHub issue #364's write half, read the
	// same way [builder.materializeFromRecord] reads it for the pre-walk
	// path.
	if n.RecordStore != nil {
		rec, _, _, identityFound, err := n.RecordStore.GetIdentity(ctx, addr)
		if err != nil {
			diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read a persisted record",
				fmt.Sprintf("Reading the record for %s failed: %s.", addr, err)))
			return providers.ImportTarget{}, false, diags
		}
		if identityFound {
			if target := importTarget(wanted{addr: addr, importID: rec.ImportID, values: rec.Components}, schema); target.IsIDBased() || target.IsIdentityBased() {
				return target, true, diags
			}
		}
	}

	// (b) The marker sweep's own index - #364's read half, computed
	// pre-walk and handed to this resolver as a snapshot (see
	// [NewMarkerIndex]).
	if target, ok := n.MarkerIndex[addr.String()]; ok {
		if target.IsIDBased() || target.IsIdentityBased() {
			return target, true, diags
		}
	}

	// (c) The identity table applied to the REAL evaluated configuration
	// value - ruling 3's own contribution, and the only one of the three
	// that could never have run before the node existed: it needs a
	// concrete value, not configuration text. Not gated by n.Unowned again
	// here - the check at the top of this function already covers it, and
	// covers (b) too; see that check's own comment for why (b) needed it
	// just as much as (c) does.
	//
	// KNOWN GAP, carried forward rather than hidden: unlike (a) and (b),
	// nothing here confirms the guessed identity actually exists before
	// reporting found=true. (a) is a record this estate's own prior
	// apply or live-import wrote; (b) is an object the marker sweep
	// actually listed; (c) is only ever "what this identity WOULD be if
	// the object exists," computed the same way [builder.materialize]'s
	// static ClassConcrete path always has. The pre-walk path can afford
	// that guess because its own import call (importAndRead, build.go)
	// deliberately treats a not-found response as an ordinary absence
	// ("where import treats a nonexistent remote object as a hard error,
	// a projection treats it as an ordinary absence" - importAndRead's own
	// doc comment). managedResourceExecute's resolver branch instead
	// reuses n.importState, the SAME hard-fail path stock's `import`
	// blocks use, on the premise that an import block's author asserted
	// the object exists - a premise this step does not actually get to
	// make. A resolver.instance table row over a genuinely new instance
	// referencing an already-applied sibling (so its value is known, not
	// unknown - the one case [identity.ComponentsFromValue] cannot tell
	// apart from "the object already exists") would therefore turn an
	// ordinary create into a fatal plan failure instead of the informative
	// static refusal this replaces. It is safe for every estate this unit
	// measures (the gauntlet's test_plan stage always runs against an
	// estate cold_deploy already applied in full, so every instance this
	// step reaches already exists), and ruling 3 itself names this step
	// with no caveat, so it ships - but making it safe for a
	// not-yet-applied estate needs an absence-tolerant import for a
	// resolver-supplied (as opposed to an operator-asserted `import`
	// block's) target, which is node engine work beyond this unit's
	// "identity only" scope. Flagged here rather than fixed so the next
	// unit does not have to rediscover it.
	row, hasRow := identity.LookupType(addr.Resource.Resource.Type)
	if hasRow {
		if importID, values, cok := identity.ComponentsFromValue(row, config); cok {
			w := wanted{addr: addr, values: values}
			if !row.IdentityObjectOnly {
				w.importID = importID
			}
			if target := importTarget(w, schema); target.IsIDBased() || target.IsIdentityBased() {
				return target, true, diags
			}
		}
	}

	// Nothing found. Ruling 4 (#365) governs exactly ONE shape of absence,
	// and it is not "nothing found" in general: a CONFIG-IDENTIFIED type -
	// one with a table row that is neither ServerAssigned nor RecordBacked,
	// so its identity is ordinarily derivable from configuration - whose
	// derivation nonetheless failed for this instance. That, and only
	// that, is the ambiguity the ruling is about: this run cannot tell
	// "genuinely new" apart from "real, and this run simply cannot derive
	// its identity yet," and creating a duplicate of a real object is the
	// one failure HANDOFF's safety rule names as worse than a refusal.
	//
	// Every other type reaching here - server-assigned (an EC2 instance, a
	// VPC: minted at create time, with no configuration argument to have
	// derived from in the first place), record-backed, or simply absent
	// from the table altogether - has no such source to be missing, and a
	// brand-new instance of one is the ORDINARY case, not an ambiguous
	// one: found=false with no diagnostic here, so managedResourceExecute
	// falls through to stock's own create behavior exactly as it does
	// without this seam. Getting this wrong once, by refusing every
	// greenfield instance in an estate that has never had ANY source, is
	// what a real gauntlet run of reference-ec2-vpc under this flag
	// caught - see this file's own tests, which now pin the boundary by
	// address.
	//
	// A fourth case joins those three, keyed the same way: a
	// CONFIG-IDENTIFIED type whose own identity argument reads a sibling
	// this SAME run has not applied yet - a genuinely new estate's own
	// aws_dynamodb_table.this[0], say, whose `name` reads
	// random_pet.this.id before random_pet has ever been created.
	// [identity.ComponentsUnknown] reports this precisely: not "the value
	// is there and unusable" (an ordinary derivation failure, still
	// ambiguous, still refused) but "the value does not exist for ANYONE
	// yet," the same reason resolve.go's static evaluator classifies this
	// instance [identity.ClassParentDerived] rather than concrete. There
	// is no candidate identity string here for a real, undiscovered
	// object to have collided with - ruling 4's whole ambiguity is about
	// telling "genuinely new" apart from "real, undiscoverable," and a
	// value nothing has computed yet cannot be either one. Stock plans the
	// same resource the same way, the attribute shown "(known after
	// apply)"; this is that, not a widened create.
	//
	// A fifth case, [identity.ComponentsServerAssignedIfAbsent]: a
	// CONFIG-IDENTIFIED type's identity-relevant argument is genuinely
	// absent (not unknown - the fourth case's own business), and the
	// provider's own Argument Reference documents that IT assigns the
	// argument when configuration leaves it blank (the *_prefix
	// convention: aws_iam_role.this's `name`, say, when `name_prefix` is
	// used instead). There is no configuration value here to have derived
	// a guess from in the first place, so this is the same "no source to
	// be missing" shape a whole-type ServerAssigned row already gets
	// exempted for above - just discovered one component at a time.
	// Caught by corpus-autoscaling-complete's own greenfield stage:
	// aws_iam_role.this and aws_sqs_queue.this both use this convention
	// (use_name_prefix defaults to true in the upstream module), and their
	// name argument's value is a known null, not unknown.
	sourceExpected := hasRow && !row.ServerAssigned && !row.RecordBacked &&
		!identity.ComponentsUnknown(row, config) &&
		!identity.ComponentsServerAssignedIfAbsent(row, config)
	if !sourceExpected {
		return providers.ImportTarget{}, false, diags
	}
	if n.NoSourceCreate {
		return providers.ImportTarget{}, false, diags
	}
	diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "No source for this instance's identity", fmt.Sprintf(
		"%s has no record in the estate's record store, no live marker, and an identity this run cannot derive from its own evaluated configuration, even though %s's identity is ordinarily computable from configuration. Run \"choudoufu live-import\" from the stock state that already holds it, or set strict { no_source_create = \"create\" } to plan a create instead.",
		addr, addr.Resource.Resource.Type,
	)))
	return providers.ImportTarget{}, false, diags
}
