// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"log"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/hashicorp/hcl/v2"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/live/cloudcontrol"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/listclient"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/moved"
	"github.com/intentius/choudoufu/internal/live/policy"
	"github.com/intentius/choudoufu/internal/live/projection"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/live/registry"
	"github.com/intentius/choudoufu/internal/live/staterecord"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Request is one discovery pass.
type Request struct {
	// Estate is the tofu-estate value that identifies this estate's
	// resources. It must satisfy the marker spec's grammar.
	Estate string

	// Config is the configuration the resolutions came from. It is what
	// says a needs-discovery instance really is declared, so that a
	// resolution list and a configuration that have drifted apart produce
	// an error rather than a binding onto nothing.
	Config *configs.Config

	// RecordBackedAddrs is edge 3 of the plan-node seam
	// (rfc/20260823-foundation-order-ruling.md, ruling 3; GitHub issue
	// #388), keyed by [addrs.AbsResourceInstance.String]. An address listed
	// here is excluded from the per-instance binding demand
	// [declaredInstances] builds from a ClassNeedsDiscovery resolution -
	// this pass will neither try to match it against a scanned marker nor
	// raise a "declared but no marker found" problem for it - because
	// something else already answered its identity for this run: under
	// CHOUDOUFU_NODE_RESOLVE=1, [projection.NodeResolver]'s own record step
	// answers it directly, at plan-node time, from the same estate record
	// this set is built from (see internal/command's
	// statelessRecordBackedNeedsDiscoveryAddrs). The instance still
	// contributes to [declared.declares] - see the first loop in
	// [declaredInstances] - so it is never misread as an orphan by this or
	// any other pass; only the wasted binding ATTEMPT is skipped.
	//
	// It does not touch the estate-wide sweep (Sweep, SweepTypes) at all:
	// that pass still lists every admitted type looking for markers nobody
	// declares, exactly as before, because an address can only appear here
	// once identity resolution has already classified it as
	// ClassNeedsDiscovery for a DECLARED resource - the sweep's job is
	// finding what nothing declares.
	//
	// Nil (the default) matches every caller before this field existed:
	// nothing is excluded, and the demand this pass builds is unchanged.
	RecordBackedAddrs map[string]bool

	// DeposedRecords is GitHub issue #361's crash-window recovery input,
	// keyed by [addrs.AbsResourceInstance.String] and then by the deposed
	// object's own key (states.DeposedKey's string form): every deposed
	// object this estate's record names, for the declared addresses a
	// caller chose to ask about. Populated in
	// internal/command/live_plan.go from the estate's already-open record
	// store, before [Discover] runs - see that file's own comment for why
	// this has to happen ahead of the scan rather than lazily inside it.
	//
	// Consulted only in bind()'s collision branch (two-or-more claimants
	// for one declared address - exactly the shape a create-before-destroy
	// crash produces while the new and old object both still carry the
	// address's marker): if exactly one claimant matches one recorded
	// deposed candidate for that address, it is pulled out of collision
	// consideration and reported in [Result.DeposedBindings] instead, and
	// the remaining single claimant binds through the ordinary case-1
	// path. Zero matches, or more than one, still raise
	// [ProblemCollision] exactly as before this field existed.
	//
	// Nil (every caller before this field existed) disables the
	// disambiguation entirely: every collision raises [ProblemCollision],
	// byte-identical to before.
	DeposedRecords map[string]map[string]projection.DeposedRecord

	// Resolutions is the identity package's output for the whole
	// configuration, needs-discovery instances included. The
	// needs-discovery ones drive the scan; the rest ride through untouched
	// so that [Result.Resolutions] is a complete list.
	Resolutions []identity.Resolution

	// Provider is a configured provider handle that speaks the list
	// protocol - the same handles listclient takes, which in practice means
	// *plugin.GRPCProvider or *plugin6.GRPCProvider.
	Provider any

	// Region is the region to list in, passed to any list configuration
	// that accepts a region argument. Empty leaves it unset, which lets the
	// provider's own configured region stand.
	Region string

	// CollectUnclaimed widens every list from "this estate's resources" to
	// "every resource of the type", so that resources carrying no estate
	// marker are visible in [Result.Unclaimed]. It costs a wider scan and
	// gives up server-side filtering, and it is what P2.4 sets.
	CollectUnclaimed bool

	// Sweep asks for the estate-wide sweep as well as the config-driven
	// scan: every admitted resource type the configuration no longer
	// declares is listed too, looking for this estate's markers.
	//
	// Without it, discovery can only see the types something in
	// configuration is waiting on, which means deleting a whole resource
	// block makes its live resources invisible rather than doomed. See
	// [sweepTypes] for which types are covered and why that list is the
	// right one.
	Sweep bool

	// SweepTypes overrides the sweep's type universe. Empty means the
	// default in [sweepTypes], which is the admission table. It exists for
	// tests and for a future in which the admitted set is derived from
	// provider schemas rather than asserted.
	SweepTypes []string

	// CloudControl is the AWS Cloud Control client a scan falls back to for
	// a marker-path type with no native provider list resource (issue #47).
	// The provider's own list resource stays primary wherever it exists -
	// it returns the provider-shaped objects the rest of discovery already
	// consumes - so this is consulted only when [listclient.Schemas] has no
	// entry for the type. Nil (the zero value) disables the fallback
	// entirely: such a type keeps refusing exactly as it did before this
	// existed, [ProblemTypeNotListable].
	CloudControl *cloudcontrol.Client

	// Roster is the live/mapping.json + live/registry.json join
	// ([registry.Roster]) that says which CFN type a TF type corresponds
	// to and whether Cloud Control can enumerate it with no required
	// input. Required for the Cloud Control fallback to fire at all; a
	// non-nil CloudControl with a nil Roster finds no type mapped and
	// behaves exactly as if CloudControl were nil too.
	Roster *registry.Roster

	// Tagging is the Resource Groups Tagging API client
	// ([cloudcontrol.NewTagging]) the sweep uses instead of per-type
	// listing when [Request.TaggingSweep] is set (issue #51). Nil disables
	// the tagging sweep regardless of TaggingSweep, the same "absence is
	// off" rule [Request.CloudControl] follows.
	Tagging *cloudcontrol.Client

	// TaggingSweep replaces the estate-wide sweep's per-type listing
	// ([sweepTypes], one list call per admitted type not already covered by
	// the config-driven scan) with one paginated GetResources call filtered
	// to this estate's tofu-estate tag, joining each returned ARN back to a
	// (TF type, identifier) pair ([joinTaggedResource]). Per-type
	// refinement - the taggable check, the malformed-marker and collision
	// rules, everything [scanTypeCloudControl] already does once a type's
	// candidates are in hand - stays exactly as it is; only how the
	// candidates are gathered changes, from N list calls to one.
	//
	// Requires both this and Tagging to be set, and Roster (the ARN join's
	// CFN-type-to-TF-type step reads it). Default off: a caller that never
	// sets it, or sets it with Tagging or Roster left nil, gets the pre-#51
	// per-type sweep unchanged.
	TaggingSweep bool

	// Policy is GitHub issue #67's resolved ownership policy. Nil means no
	// policy block at all (or one that set nothing this package reads),
	// which resolves to [policy.DefaultVerb] for every quadrant and leaves
	// this pass's behavior exactly as it was before Policy existed. Set,
	// it governs the undeclared_tagged quadrant: which of the removals
	// [classifyOrphans] and [parentReadSweep] proposed are actually kept as
	// destroys, in [applyOrphanPolicy].
	Policy *policy.Policy

	// ScopeProvider narrows which of Resolutions this pass treats as
	// "waiting to be found" - both the needs-discovery config-driven scan
	// and the parent-read leg - to the ones whose resource block uses this
	// provider configuration. It exists for issue #69's multi-provider
	// sweep ([Merge], run by internal/command/live_plan.go's
	// statelessDiscover once per distinct provider configuration among an
	// estate's managed resources): a pass must never bind a needs-discovery
	// instance, or read a parent-derived child, through the wrong account.
	//
	// It deliberately does NOT narrow what counts as "already declared" for
	// [declared.declares] - the check that keeps a live, client-named
	// resource matching another pass's declared address from being
	// misclassified as an orphan. That check stays global, over every
	// resolution in Resolutions regardless of provider, because a type like
	// aws_s3_bucket whose list operation is account-global (not
	// region-scoped - see testdata/alias-e2e/main.tf's comment) will hand
	// every pass every account's bucket, including ones declared under a
	// different provider configuration; without whole-config knowledge of
	// what is declared, a pass would misread another provider's own
	// resource as undeclared and propose destroying it out from under the
	// pass that actually owns it.
	//
	// The zero value means no scoping: every resolution in Resolutions is
	// in scope, which is every existing caller's behavior and what keeps a
	// single-provider Discover call exactly as it always was.
	ScopeProvider addrs.AbsProviderConfig

	// ---------------------------------------------------------------------
	// Guided discovery (issue #64's second leg)
	// ---------------------------------------------------------------------

	// Guided opts the estate-wide sweep into consuming the most recent
	// hint the estate's record store carries (issue #109; written by
	// [projection.Manager] after every apply's final persist) as a cost
	// HINT: which admitted types this estate has ever held, so the sweep's
	// routine pass can skip re-listing a type with no evidence behind it
	// instead of paying one List call per admitted type on every plan.
	// Default off in this package: a direct caller of [Discover] that
	// never sets this gets exactly today's full enumeration, unchanged.
	// The fork's own commands (internal/command's statelessDiscover) turn
	// it on automatically instead of leaving it at the zero value - see
	// the policy note in guided.go's file doc comment for exactly when,
	// and with what defaults.
	//
	// The hint is never authority. A type absent from the hint is always
	// swept in full, on every run - see guidedSweepUniverse - and any
	// problem reading the hint (no store configured, no hint recorded yet,
	// a corrupted one, one older than GuidedMaxAge) falls back to full
	// enumeration silently: [Result.GuidedFallback] names why, and
	// discovery never returns an error for it. TestGuided_equivalence pins
	// the load-bearing half of that promise: guided discovery, given any
	// such problem, produces byte-identical output to Guided: false over
	// the same estate.
	//
	// Guided only narrows the sweep of undeclared types. The config-driven
	// scan (every type something in configuration is waiting on) is
	// unaffected either way: it already lists only this estate's own
	// resources, server-side filtered wherever the type supports it, so
	// there is no per-run universe for a hint to narrow there.
	Guided bool

	// HintStore is the estate's record store, ordinarily the same store
	// [Request]'s caller opened from the live block's record_store block. It
	// carries the guided hint (issue #109, read through
	// [projection.ReadHintStore] at the key [projection.HintKey](Estate)
	// derives) and, independent of Guided, backs
	// [scanTypeLocatedFallback]'s per-instance identity lookups for a type
	// with no tags argument and no list route: [projection.NewRecordEnvelopeStore]
	// wraps it, at [KeyPrefix], the same way internal/command's projection
	// build does. A caller sets this whether or not it also turns Guided on
	// - the two consume the same handle for two different questions, and a
	// nil value disables both: the hint read falls back to full
	// enumeration, and the located fallback finds nothing to consult,
	// exactly as before either existed.
	HintStore staterecord.Store

	// KeyPrefix is the namespace [scanTypeLocatedFallback] reads a located
	// identity under - ordinarily [projection.RecordKeyPrefix](Estate), or
	// a record_store block's key_prefix override, exactly matching what the
	// caller's own projection build used for [projection.Options.RecordStore].
	// GitHub issue #364 merged the located, residue and provisioner-taint
	// namespaces into the same one an ordinary record-backed instance's
	// value lives under, so an override that moves the record namespace now
	// moves this population with it, and the two must agree on where to
	// look or a migration's written identity becomes invisible to this
	// fallback. Empty resolves to [projection.RecordKeyPrefix](Estate), the
	// only value a caller that predates this field's addition could mean.
	KeyPrefix string

	// GuidedMaxAge is how old a hint may be before guided discovery treats
	// it exactly like a missing one: readable, but not trusted. Zero uses
	// defaultGuidedMaxAge. Staleness beyond this bound falls back to full
	// enumeration for the same reason a missing hint does - see
	// TestGuided_equivalence's stale case.
	GuidedMaxAge time.Duration

	// GuidedVerify forces this pass to fully sweep every admitted type even
	// when a fresh hint would otherwise narrow the routine sweep - the
	// "periodic or flagged full sweep" that re-verifies the hint set, so a
	// resource of a hinted type created out of band still surfaces. Discover
	// is stateless between calls; a caller owns the cadence (e.g. "every
	// 10th plan" or an explicit -verify flag) and sets this when that
	// cadence says so. Ignored when Guided is false.
	GuidedVerify bool

	// GuidedVerifyAge is an age-based, automatic form of GuidedVerify: a
	// hint younger than GuidedMaxAge (so still trusted enough to narrow the
	// sweep) but older than this runs the pass as a full sweep anyway - same
	// effect as GuidedVerify, but decided from the hint's own age rather
	// than a caller-tracked cadence. Zero disables it, which leaves
	// GuidedVerify as the only lever and matches every behavior this field
	// did not exist to change. See internal/command's statelessDiscover for
	// the default this fork's own commands set when they turn guided
	// discovery on automatically - the "drift never hides longer than a
	// day" half of that policy is this field, not GuidedMaxAge.
	GuidedVerifyAge time.Duration

	// Progress, when set, is called once after every resource type this
	// pass scans - both the config-driven scan and the estate-wide sweep -
	// with a cumulative count of types scanned and live resources found so
	// far. It exists so a caller working against a large estate can render
	// a heartbeat while discovery runs, which otherwise stays silent for as
	// long as the sweep takes: the admission table can run to hundreds of
	// types, each a separate list call. Nil (the default) disables it, and
	// every existing caller behaves exactly as it always has.
	//
	// The callback runs synchronously on the discovery goroutine, in
	// between list calls, so it must not block - a caller wanting to
	// throttle how often it actually prints something owns that decision
	// itself, since Discover reports every scan and does no throttling of
	// its own.
	Progress ProgressFunc

	// markers is the estate-filtered Tagging API answer, fetched at most
	// once per pass and shared between the config-driven scan's tag join
	// (issue #266) and the estate-wide sweep, which used to make the call
	// itself. It is unexported because [Discover] installs it from Tagging
	// and Estate; a caller neither sets nor sees it.
	markers *markerIndex
}

// ProgressFunc is a discovery progress callback. See Request.Progress.
type ProgressFunc func(ProgressEvent)

// ProgressEvent is one discovery heartbeat, cumulative since Discover
// started: the resource type just scanned, how many types have been
// scanned in total so far, and how many live resources scanning has found
// so far. Sweep is true when the type just scanned came from the
// estate-wide sweep rather than the config-driven scan.
type ProgressEvent struct {
	TypeName       string
	Sweep          bool
	TypesScanned   int
	ResourcesFound int
}

// Discover finds the live resources of an estate and binds them to the
// declared addresses that own them.
//
// The returned Result is usable even when diagnostics carry errors: the
// bindings that were unambiguous are still in it, which is what lets a
// caller report a collision alongside everything else it found. A caller
// must not build a projection from a result whose diagnostics have errors,
// because a marker problem means the estate's ownership records disagree
// with each other and a plan built on them would act on the wrong resource.
func Discover(ctx context.Context, req Request) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	res := &Result{
		Estate:      req.Estate,
		Resolutions: append([]identity.Resolution(nil), req.Resolutions...),
	}

	switch {
	case !ValidEstateName(req.Estate):
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Invalid estate name",
			fmt.Sprintf("Discovery needs the estate's name, matching the tofu-estate marker grammar in live/MARKERS.md (a lowercase letter followed by letters, digits or hyphens, at most 128 characters). Got %q.", req.Estate),
		))
	case req.Config == nil || req.Config.Module == nil:
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to discover against",
			"Discovery needs the configuration the identity resolutions were computed from, so that a marker can be matched against an address that is actually declared, and none was given.",
		))
	case req.Provider == nil:
		return res, diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider access",
			"Discovery needs a configured provider handle to list live resources with, and none was given.",
		))
	}

	// Issue #266. The one estate-filtered GetResources call the sweep used
	// to make itself is installed here instead, lazily, so the
	// config-driven scan can join its tags onto listed objects whose own
	// list call dropped them. Nil Tagging leaves this nil, and a nil index
	// answers "unavailable" to everything - the pre-#266 behavior, which is
	// also what TOFU_LIVE_CLOUDCONTROL=off buys.
	req.markers = newMarkerIndex(req)

	decl, declDiags := declaredInstances(ctx, req)
	diags = diags.Append(declDiags)
	if declDiags.HasErrors() {
		return res, diags
	}
	if len(decl.types) == 0 && len(decl.recordBacked) == 0 && !req.Sweep {
		// Nothing waits on discovery and no sweep was asked for, which is a
		// legitimate configuration: every instance was named by static
		// analysis, and without a sweep there is nothing else to look at.
		res.sortEverything()
		return res, diags
	}
	// decl.recordBacked non-empty but decl.types empty: every needs-discovery
	// instance in this configuration is record-backed. There is still slot
	// bookkeeping to do for any of them that sit in a count block (see
	// recordBacked's own doc comment), which happens below in bind() via
	// [declared.bindTypeNames] - but nothing here calls the provider's list
	// endpoint, because decl.typeNames() (the scan loop just below) is
	// unchanged: schemas are fetched (a local schema call, not a list call)
	// and the scan loop runs zero iterations.

	schemas, schemaDiags := listclient.ListSchemas(ctx, req.Provider)
	diags = diags.Append(schemaDiags)
	if schemaDiags.HasErrors() {
		return res, diags
	}

	// typesScanned and resourcesFound are progress's running totals across
	// both loops below, reported through Request.Progress after each type -
	// see scanTypeReporting.
	var typesScanned, resourcesFound int

	for _, typeName := range decl.typeNames() {
		diags = diags.Append(scanTypeReporting(ctx, req, schemas, decl, typeName, res, false, &typesScanned, &resourcesFound))
	}

	// The sweep runs after the config-driven scan so that a type appearing
	// in both is scanned once, on the terms the configuration set.
	if req.Sweep {
		if req.TaggingSweep && req.Tagging != nil && req.Roster != nil {
			// Issue #51: one estate-wide GetResources call replaces the
			// per-type loop below, for every type [partitionSweepTypes]
			// doesn't carve out. See [sweepViaTagging] and
			// [Request.TaggingSweep]. It is one round trip rather than one
			// per type, so it gets no progress events of its own - there is
			// nothing to report between, only before and after.
			taggingUniverse, nativeUniverse := partitionSweepTypes(req, decl)
			diags = diags.Append(sweepViaTagging(ctx, req, decl, res, taggingUniverse))
			// Issue #394: a companion pair whose identities diverge
			// ([typeNeedsResourceObjectToRecompose]) can only ever bind
			// through a native list call's own resource object, which the
			// tag sweep's ARN-joined candidate never carries - so these few
			// types still go through the per-type loop even though
			// TaggingSweep is set.
			for _, typeName := range nativeUniverse {
				diags = diags.Append(scanTypeReporting(ctx, req, schemas, decl, typeName, res, true, &typesScanned, &resourcesFound))
			}
		} else {
			// #64's guided leg: guidedSweepUniverse returns sweepTypes(req,
			// decl) unmodified (and an empty fallback reason) whenever
			// Request.Guided is false, so this is a no-op for every
			// existing caller. See the Request.Guided doc comment and
			// guided.go for what changes when it is set.
			universe, skipped, fallback := guidedSweepUniverse(ctx, req, decl)
			res.Guided = req.Guided && fallback == ""
			res.GuidedFallback = fallback
			res.GuidedSweepSkipped = skipped
			for _, typeName := range universe {
				diags = diags.Append(scanTypeReporting(ctx, req, schemas, decl, typeName, res, true, &typesScanned, &resourcesFound))
			}
		}
	}

	diags = diags.Append(bind(req, decl, res))
	diags = diags.Append(classifyOrphans(req, res))

	// The parent-read leg (issue #60) runs after bind and classifyOrphans:
	// it reads res.Resolutions to find both which parent instances this
	// pass resolved and which children are already declared, and both are
	// only settled once binding and orphan classification have run.
	if req.Sweep {
		diags = diags.Append(parentReadSweep(ctx, req, schemas, res))
		// The fold-child leg (issue #68) runs right after: same res.Resolutions
		// vantage point, generalized to a parent that may itself be
		// untaggable and composite rather than concrete. See
		// internal/live/discovery/fold_read.go's package doc comment.
		diags = diags.Append(foldChildReadSweep(ctx, req, schemas, res))
		// The record-orphan-read leg (issue #364 ruling item 1's removal
		// half) runs last of the three: it needs res.Resolutions AND
		// res.Unbound settled the same way classifyOrphans's own
		// rename-safety check does. See
		// internal/live/discovery/recordorphan_read.go's package doc
		// comment.
		diags = diags.Append(recordOrphanReadSweep(ctx, req, schemas, res))
	}

	// Policy narrows the undeclared_tagged quadrant last, once every removal
	// classifyOrphans and parentReadSweep would otherwise propose is
	// settled: see [applyOrphanPolicy].
	applyOrphanPolicy(req, res)

	res.sortEverything()
	return res, diags
}

// DeclaredDiagnostics runs the provider-free half of discovery: it checks
// every needs-discovery resolution against the declared configuration and
// reports the diagnostics that follow from that alone - a marker address
// too long to carry, two declared addresses that escape to the same marker
// value, and a resolution naming a resource block the configuration no
// longer declares. See [declaredInstances], which this calls directly.
//
// [Discover] refuses to run at all without req.Provider, because listing
// live resources is the whole reason it exists. That gate sits in front of
// declaredInstances too, even though declaredInstances itself never reads
// req.Provider - it works from req.Config and req.Resolutions (and
// req.ScopeProvider, for issue #69's multi-provider sweep) alone. This
// entry point exists so a caller with no provider handle, such as an
// offline check, can still run the part of discovery that needs none.
//
// req.Provider is ignored - callers with no provider handle at all are
// exactly who this exists for. req.Config must be non-nil: unlike Discover,
// this does not check that first, because declaredInstances dereferences it
// for the root module (identity.ConfigForModule(nil, root) hands back a nil
// *configs.Config with ok=true, and the field access after it - modCfg.Module
// - panics on that nil rather than degrading). A nil or moduleless Config is
// refused here for the same reason Discover refuses it.
func DeclaredDiagnostics(ctx context.Context, req Request) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	if req.Config == nil || req.Config.Module == nil {
		return diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to discover against",
			"Discovery needs the configuration the identity resolutions were computed from, so that a marker can be matched against an address that is actually declared, and none was given.",
		))
	}
	_, diags = declaredInstances(ctx, req)
	return diags
}

// sweepTypes is the estate-wide sweep's type universe: every type the
// stateless admission table covers that the config-driven scan did not
// already list.
//
// The admission table is the right source and the only one available. There
// is no record of what an estate contains - that record is the thing this
// fork removes - so the question "which types might this estate own" has to
// be answered from a rule rather than from memory. The rule the whole fork
// already runs on is admission: lint refuses a configuration that declares a
// type outside the table, so every resource an estate acquired through
// stateless mode is of an admitted type. Sweeping the admission table is
// therefore complete over everything this tool can have created, and it
// costs a bounded, small number of list calls (twenty-six types today) rather
// than the ~180 a whole-provider sweep would take.
//
// What it does not cover is a resource of an unadmitted type that somebody
// stamped this estate's markers onto by hand. Adoption is a tag write, so
// that is possible, and it is a documented limit rather than a silent one:
// the markers are the contract, and the admission table is the list of types
// the contract is defined over.
//
// The other two candidate designs were weighed and rejected. Sweeping every
// listable type the provider offers is complete against hand-stamping too,
// but it turns every plan into ~180 list calls against the account, most of
// them for types no estate will ever hold; the sweep would dominate the cost
// of a run and the sweep's own failures would dominate its output. Sweeping
// "the types recorded as belonging to this estate" is the design that cannot
// exist here: the recording of it would be a store, which is exactly the
// thing whose absence is the point.
//
// One class of admitted type is excluded outright: a record-backed one
// ([identity.TypeIdentity.RecordBacked]). See [cloudObservable].
func sweepTypes(req Request, decl *declared) []string {
	universe := req.SweepTypes
	if len(universe) == 0 {
		universe = identity.AdmittedTypes()
	}
	out := make([]string, 0, len(universe))
	for _, t := range universe {
		if decl.types[t] != nil {
			continue
		}
		if !cloudObservable(t) {
			continue
		}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// cloudObservable reports whether a resource type can have a live cloud
// object at all, which is the precondition for asking a provider to list it.
//
// It is false for exactly the record-backed types (GitHub issue #73's
// RECORD_ADMITTED logical types: null_resource, terraform_data, and the
// random_* and time_* families). Those have no cloud counterpart by
// construction - choudoufu persists them itself, per estate, and
// internal/live/projection materializes an instance from that record without
// any cloud read at all ([identity.ClassRecordBacked], build.go's
// materializeRecord). Listing one is asking the account about something that
// was never there, and the provider handle discovery lists through has no
// list resource for it either way, so every such request came back as a
// [SweepGapNotListable] gap: fourteen of them on every swept plan, each one
// telling an operator that removal coverage has a hole where no coverage was
// ever possible or needed. A deleted null_resource block is the record
// store's business, never the sweep's.
//
// The rule is read off the admission table rather than named type by type,
// so a row row-gen marks RecordBacked later is excluded the day it lands.
// The universe is filtered whoever asked for it, including an explicit
// [Request.SweepTypes]: "has no cloud object" is a property of the type, not
// of the caller.
func cloudObservable(typeName string) bool {
	entry, ok := identity.LookupType(typeName)
	if !ok {
		// Outside the table entirely. sweepTypes' own universe cannot
		// contain such a type, but an explicit Request.SweepTypes can, and
		// "unknown" is not "record-backed" - leave it to the scan, which
		// reports what it finds.
		return true
	}
	return !entry.RecordBacked
}

// ---------------------------------------------------------------------------
// Declared instances
// ---------------------------------------------------------------------------

// declaredEntry is one declared needs-discovery instance and the live
// resources that claimed it.
type declaredEntry struct {
	res       identity.Resolution
	escaped   string
	claimants []claimant

	// inCount is set for an instance of a count block, whose binding is the
	// set matcher's business rather than this entry's own.
	inCount bool

	// recordBacked is set for an entry filed under [declared.recordBacked]
	// rather than [declared.types] - this instance's identity already came
	// from the estate's record (edge 3, GitHub issue #388), so it was
	// deliberately excluded from the scan and will always have zero
	// claimants here, by construction, not because nothing live answers
	// for it. bindCountByAddress and bindCountBySlot read this so a
	// zero-claimant record-backed entry is never appended to
	// [Result.Unbound] - see those functions' own comments for what
	// happens when it is: classifyOrphans's rename guard withholds a
	// genuine removal of a SIBLING block sharing the same [blockKey],
	// because "an unclaimed declared instance of this block" is exactly
	// what Unbound is read to mean, and a record-backed instance is
	// neither unclaimed nor a candidate for a rename pairing.
	recordBacked bool
}

// claimant is one live resource carrying a marker that named a declared
// address.
type claimant struct {
	importID     string
	identityAttr string
	identity     cty.Value
	displayName  string
	marker       string
	escaped      string
	normalized   bool
	slot         string
	tags         map[string]string
	noIdentity   bool
}

// displayID is how a claimant is named in a message: its live identity, or a
// stand-in when the provider sent none. A resource with no identity still has
// to appear in a problem, since "one of these is unidentifiable" is
// information.
func (c claimant) displayID() string {
	if c.importID == "" {
		return "(no identity)"
	}
	return c.importID
}

// declaredBlock is a resource block with expanded instances, used to
// recognize a marker that names the block rather than one of its instances.
type declaredBlock struct {
	addr      string // escaped block address, e.g. "aws_eip.pool"
	instances int
	keyed     bool
	claimants []claimant
}

// declared is the configuration side of the binding: which addresses of
// which types are waiting to be found.
type declared struct {
	types  map[string]map[string]*declaredEntry // type -> escaped instance address -> entry
	blocks map[string]map[string]*declaredBlock // type -> escaped block address -> block
	counts map[string]map[string]*countBlock    // type -> escaped block address -> count block

	// typeAliases is the instance addresses a moved block says a declared
	// instance used to have (GitHub issue #198), kept out of types for the
	// same reason countAliases is kept out of counts: indexCountBlocks
	// enumerates types to hang a count block's declared instances off it,
	// and an entry reachable under two keys there would be hung on twice -
	// which made a two-instance set report four declared instances and bind
	// both live members to index 0. See [declared.entryFor].
	typeAliases map[string]map[string]*declaredEntry

	// countAliases is the block addresses a moved block says a count block
	// used to have (GitHub issue #198), kept out of counts on purpose: that
	// map is enumerated to bind the sets, and a block reachable under two
	// keys there would be bound once per key. See [declared.countBlockFor].
	countAliases map[string]map[string]*countBlock

	// recordBacked is [declaredEntry] objects for instances excluded from
	// the binding demand by [Request.RecordBackedAddrs] (edge 3, GitHub
	// issue #388 - see that field's own doc comment), keyed the same way
	// types is. Deliberately kept out of types: types drives both the
	// config-driven scan's demand ([declared.typeNames], the "any work to
	// do" shortcut in [Discover]) and marker-matching lookups
	// ([declared.entryFor]), and an entry here must join neither - that is
	// the "wasted binding ATTEMPT" the doc comment on RecordBackedAddrs
	// says is the only thing skipped. What it must still join is
	// [countBlock.entries]: [declared.indexCountBlocks] walks this map as
	// well as types, purely to mint or carry that instance's tofu-slot the
	// same way an ordinary zero-claimant entry does (bindCountByAddress's
	// and bindCountBySlot's `case 0`/deficit path). Without this, a
	// record-backed count instance vanishes from the per-instance loop the
	// slot binder walks entirely, so it never gets a [SlotAssignment] and
	// [Result.SlotTable] has no entry for it - see GitHub issue #388's
	// flag-sweep-scout comment for the two estates that regressed this way.
	recordBacked map[string]map[string]*declaredEntry

	order map[string][]string // type -> escaped instance addresses, in address order

	// unscanned holds the types whose scan never happened - the provider
	// cannot list them, or listing errored. Their declared instances are
	// not reported as unbound, because "nothing claims this address" and
	// "nobody looked" are different answers and only the first one means
	// the plan should propose creating something.
	unscanned map[string]bool

	// unreadable counts, per type, the live resources the config-driven
	// scan listed and could not read an ownership marker off - after the
	// tag join (issue #266) had its chance at them.
	//
	// It is the evidence behind [unreadableMarkerProblem]: an instance that
	// goes unbound while this is zero for its type has no live resource
	// anywhere the run looked, so a create is the right plan. An instance
	// that goes unbound while this is non-zero may be looking straight at
	// its own resource without being able to tell. Sweep scans do not count
	// here - a sweep lists types the configuration declares nothing of, so
	// no instance of them could be waiting on one.
	unreadable map[string]int

	// all is the escaped address of every declared instance in the
	// configuration, whatever its identity class - not only the ones waiting
	// on discovery - indexed by resource type.
	//
	// It exists for the sweep, and it is the difference between a sweep and
	// a catastrophe. The sweep lists types nothing is waiting on, which is
	// mostly the client-named ones: a live bucket carrying the marker
	// aws_s3_bucket.data is not claiming anything the scan was looking for,
	// because that instance's identity came out of the configuration and it
	// was never in the discovery list at all. Judging it by that alone would
	// make it an orphan, and orphans are destroyed. Membership here is what
	// says "this address is declared, by something that did not need
	// finding", and it is checked before anything is called undeclared.
	//
	// The type key is load-bearing rather than tidiness (audit finding C4). A
	// flat set of escaped addresses answers "is this string declared
	// somewhere" and not "is this live resource's marker its own address", so
	// a subnet tagged with an EIP's address matched the set and was dropped
	// on the floor - neither bound, nor orphan, nor problem, which is a hole
	// in the owned/malformed/foreign trichotomy the marker spec is built on.
	//
	// The value is the resolution that declared the address rather than a
	// bare bool (GitHub issue #244, half 2). Membership alone answers "is
	// this address declared by SOMETHING", which is not the question a live
	// object's marker asks: it asks "is this object the instance that address
	// names". Only the resolution carries the identity the configuration
	// computes, and only holding it here can [declared.displacedFrom] tell
	// the two apart. See [declaredAddress].
	all map[string]map[string]*declaredAddress
}

// declaredAddress is one escaped marker value the configuration declares, and
// the resolution that declared it.
//
// ambiguous records that more than one resolution of this type escapes to
// this same marker value, which can happen when a `moved` block's origin for
// one instance is another instance's own address in a different module. Where
// it is set nothing here can say which instance a marker naming it means, so
// every question beyond "is it declared" is answered "do not know" - the
// answer that leaves today's behaviour exactly as it was.
type declaredAddress struct {
	res       identity.Resolution
	ambiguous bool
}

// declares reports whether the configuration declares the given escaped
// instance address for the given resource type.
func (d *declared) declares(typeName, escaped string) bool {
	return d.all[typeName][escaped] != nil
}

// record files one resolution's escaped address in [declared.all], marking
// the entry ambiguous if a different instance already claimed the same
// marker value.
func (d *declared) record(typeName, escaped string, r identity.Resolution) {
	if d.all[typeName] == nil {
		d.all[typeName] = make(map[string]*declaredAddress)
	}
	if existing := d.all[typeName][escaped]; existing != nil {
		if existing.res.Addr.String() != r.Addr.String() {
			existing.ambiguous = true
		}
		return
	}
	d.all[typeName][escaped] = &declaredAddress{res: r}
}

// entryFor returns the declared instance a marker value names: the instance
// whose own escaped address it is, or - for a marker a moved block says now
// belongs to a different address (GitHub issue #198) - the instance it moved
// to. The canonical index is consulted first, so a declared address always
// beats an alias and a moved block can never redirect a live resource away
// from an instance the configuration still declares.
func (d *declared) entryFor(typeName, escaped string) (*declaredEntry, bool) {
	if entry, ok := d.types[typeName][escaped]; ok {
		return entry, true
	}
	entry, ok := d.typeAliases[typeName][escaped]
	return entry, ok
}

func (d *declared) typeNames() []string {
	out := make([]string, 0, len(d.types))
	for t := range d.types {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// bindTypeNames is typeNames widened by the types that have NO scan demand
// at all - every needs-discovery instance of them is record-backed - but
// still have a count block to bind, purely for slot accounting (see
// recordBacked's own doc comment). [bind] uses this instead of typeNames so
// that a count block entirely answered from the record still gets its
// tofu-slot minted or carried; [Discover]'s scan loop and its "nothing to
// do" shortcut use typeNames unchanged, so a type in this widened set but
// absent from typeNames still triggers no scan and no provider call.
func (d *declared) bindTypeNames() []string {
	out := make([]string, 0, len(d.types)+len(d.recordBacked))
	seen := make(map[string]bool, len(d.types)+len(d.recordBacked))
	for t := range d.types {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	for t := range d.recordBacked {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Strings(out)
	return out
}

// inScope reports whether a resource block is this pass's business: every
// block, when scope is the zero value (every existing caller, unchanged),
// or only the blocks using that exact provider configuration when it is
// set (issue #69's multi-provider sweep - see [Request.ScopeProvider]'s own
// doc comment for what this is and is not used to restrict). modCfg is the
// [configs.Config] node for the static module the block itself is declared
// in, the same node [providerscope.ResolveResource] needs to walk every
// ancestor module call's `providers = {...}` mapping up to the root (GitHub
// issue #188) - a resource inside a module called with an aliased mapping
// scopes against the account or region the mapping actually sends it to,
// not the module's own local provider reference.
func inScope(scope addrs.AbsProviderConfig, rc *configs.Resource, modCfg *configs.Config) bool {
	if scope.Provider.Type == "" {
		return true
	}
	addr := providerscope.ResolveResource(modCfg, rc)
	return addr.String() == scope.String()
}

// declaredInstances indexes the needs-discovery resolutions by type and
// escaped address, checking each against the configuration as it goes.
func declaredInstances(ctx context.Context, req Request) (*declared, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	d := &declared{
		types:        make(map[string]map[string]*declaredEntry),
		blocks:       make(map[string]map[string]*declaredBlock),
		counts:       make(map[string]map[string]*countBlock),
		typeAliases:  make(map[string]map[string]*declaredEntry),
		countAliases: make(map[string]map[string]*countBlock),
		recordBacked: make(map[string]map[string]*declaredEntry),
		order:        make(map[string][]string),
		unscanned:    make(map[string]bool),
		unreadable:   make(map[string]int),
		all:          make(map[string]map[string]*declaredAddress, len(req.Resolutions)),
	}

	// The moved blocks this configuration's markers can follow (GitHub issue
	// #198), computed once. Every alias below is derived from this list, and
	// internal/live/lint refuses exactly the statements it leaves out, so a
	// block that passes lint is a block whose old address is indexed here.
	movedStmts := moved.Honoured(req.Config)

	for _, r := range req.Resolutions {
		typeName := r.Type()
		raw := r.Addr.String()
		escaped := EscapeAddress(raw)
		d.record(typeName, escaped, r)
		for _, origin := range moved.Aliases(movedStmts, r.Addr) {
			// A pending move: this instance's live resource may still be
			// carrying the address it had before the moved block was
			// written. Recording it here is what keeps the sweep from
			// reading that resource as an orphan and planning to destroy
			// it - the whole reason a rule downgraded without this index
			// would be unsafe. [moved.Aliases] is what applies the type
			// filter a marker's own grammar requires, and it is the same
			// function internal/live/projection's ownership check reads
			// through [moved.Accepts], so the two layers cannot come to
			// disagree about which markers a moved block makes acceptable.
			//
			// The resolution filed under the origin is this instance's own,
			// which is the point: a live object still carrying the old
			// address IS this instance's object, so the identity the
			// configuration computes for the NEW address is the identity
			// that object must have. That is what keeps #244's check from
			// firing on every pending move.
			d.record(typeName, EscapeAddress(origin.String()), r)
		}
		if legacy := LegacyEscapeAddress(raw); legacy != escaped {
			// A for_each key containing "@" - the one character both the
			// pre- and post-issue-#178 grammars admit but escape
			// differently - is also declared under the address a prior run
			// would have written, so a client-named instance (the only
			// consumer of d.all) that predates the widened grammar is still
			// recognized as declared. See markers.AddressMatches's doc
			// comment.
			d.record(typeName, legacy, r)
		}
	}

	// Sorting first makes both the scan order and the reported order
	// deterministic regardless of how the caller assembled its list.
	sorted := append([]identity.Resolution(nil), req.Resolutions...)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Addr.String() < sorted[j].Addr.String()
	})

	for _, r := range sorted {
		if r.Class != identity.ClassNeedsDiscovery {
			continue
		}
		if req.RecordBackedAddrs[r.Addr.String()] {
			// Edge 3 (see [Request.RecordBackedAddrs]'s own doc comment):
			// this instance's identity is already answered from the
			// estate's record, so it never joins the binding demand below -
			// no scan match is attempted for it, and it can raise none of
			// the diagnostics the loop below raises. It was already
			// recorded as declared in the loop above, so this skip cannot
			// make it look like an orphan.
			//
			// It is filed under recordBacked, not types, so that
			// [declared.indexCountBlocks] can still hang it off its count
			// block (see recordBacked's own doc comment): a record-backed
			// count instance still needs a tofu-slot minted or carried,
			// exactly as a genuinely-unbound one does, or the slot the
			// binder assigns its siblings comes out shifted and its own
			// tofu-slot tag is left unwritten - the #388 flag-sweep-scout
			// regression on corpus-iam-policy and corpus-vpc-complete.
			//
			// Logged rather than silent so a real run's TF_LOG=debug output
			// is how the shrink is measured against a migrated estate,
			// the same way the two DEBUG lines a few hundred lines below
			// already narrate this pass's other per-instance decisions.
			log.Printf("[DEBUG] stateless/discovery: %s excluded from the binding demand: identity already recorded", r.Addr)
			typeName := r.Type()
			escaped := EscapeAddress(r.Addr.String())
			if d.recordBacked[typeName] == nil {
				d.recordBacked[typeName] = make(map[string]*declaredEntry)
			}
			d.recordBacked[typeName][escaped] = &declaredEntry{res: r, escaped: escaped, recordBacked: true}
			continue
		}
		typeName := r.Type()
		blockAddr := r.Addr.Resource.Resource.String()
		// The resource block is bound rather than discarded because the two
		// diagnostics below are about what the configuration says, and its
		// DeclRange is the only thing that can point at the block that says
		// it. The lookup is module-qualified: r.Addr.Module says which node
		// of the static tree declares blockAddr, and a block with the same
		// local name in a different module must not match.
		modCfg, modCfgOK := identity.ConfigForModule(req.Config, r.Addr.Module)
		var block *configs.Resource
		if modCfgOK && modCfg.Module != nil {
			block = modCfg.Module.ManagedResources[blockAddr]
		}
		if block == nil {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Resolved resource missing from the configuration",
				fmt.Sprintf("Discovery was asked to find %s, but the configuration it was given declares no resource block %q in %s. The resolutions and the configuration come from different runs; this is a bug in whatever assembled them.", r.Addr, blockAddr, moduleDisplay(r.Addr.Module)),
			))
			continue
		}

		if !inScope(req.ScopeProvider, block, modCfg) {
			// Declared, but by a different provider configuration than this
			// pass is scoped to (issue #69's multi-provider sweep). It
			// already contributed its address to d.all above, which is what
			// keeps another pass's sweep from mistaking it for an orphan;
			// this pass simply is not the one that tries to find it.
			continue
		}

		escaped := EscapeAddress(r.Addr.String())
		if len([]rune(escaped)) > MaxAddressLen {
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Address too long to carry an ownership marker",
				Detail:   fmt.Sprintf("The address %s escapes to %d characters, over the %d-character ceiling tofu-address and its continuation tags allow (live/MARKERS.md, MaxContinuations x MaxTagValue), so no live resource can carry it as a marker. See live/MARKERS.md; this is a lint-time error, not something discovery can work around.", r.Addr, len([]rune(escaped)), MaxAddressLen),
				Subject:  block.DeclRange.Ptr(),
			})
			continue
		}

		if d.types[typeName] == nil {
			d.types[typeName] = make(map[string]*declaredEntry)
			d.blocks[typeName] = make(map[string]*declaredBlock)
		}
		if existing, ok := d.types[typeName][escaped]; ok {
			// Two declared instances escaping to one marker value. The
			// marker spec calls this out as possible in principle (a count
			// index and a quoted key with the same digits) and impossible
			// in practice within one block; if it ever happens, binding
			// either one would be a guess.
			diags = diags.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "One marker value for two declared addresses",
				Detail:   fmt.Sprintf("%s and %s both escape to the marker value %q, so a tofu-address tag cannot tell them apart. See the escaping rule in live/MARKERS.md.", existing.res.Addr, r.Addr, escaped),
				Subject:  block.DeclRange.Ptr(),
			})
			continue
		}
		entry := &declaredEntry{res: r, escaped: escaped}
		d.types[typeName][escaped] = entry
		d.order[typeName] = append(d.order[typeName], escaped)

		if legacy := LegacyEscapeAddress(r.Addr.String()); legacy != escaped {
			// A marker a prior run wrote for this instance's key - possible
			// only for a key containing "@" - used this pre-issue-#178
			// escaping. Filing the same entry under that value too is what
			// lets an old-grammar marker still bind a live resource to this
			// instance (see markers.AddressMatches's doc comment); d.order
			// above carries only the canonical (current) key, so nothing
			// about what this run itself stamps or reports changes. If the
			// legacy value happens to already name a different declared
			// instance - only possible by the same one-in-a-huge-space
			// coincidence noted on markers.UnescapeKey - the alias is
			// skipped rather than overwritten, so it can never misdirect a
			// live resource that was never this instance's to begin with.
			if _, taken := d.types[typeName][legacy]; !taken {
				d.types[typeName][legacy] = entry
			}
		}

		// The addresses a moved block says this instance used to have
		// (GitHub issue #198). Filing the same entry under each is what
		// binds a live resource still carrying the old marker to the
		// instance that declares it now, instead of leaving the old address
		// an orphan and the new one absent - and, because claimants
		// accumulate on the entry rather than on the marker string, two live
		// resources arriving under two different addresses of the same
		// instance land in one place and come out as the collision they are.
		//
		// d.order deliberately does not learn these, exactly as it does not
		// learn the legacy-grammar alias above: the canonical address is the
		// only one this run reports or stamps. An alias that would displace
		// another declared instance's own entry is skipped rather than
		// overwritten, so a moved block can never redirect a live resource
		// away from an address the configuration still declares - a shape
		// moved.Honourable refuses outright, checked again here because a
		// silent misdirection is the one outcome worth two guards.
		for _, origin := range moved.Aliases(movedStmts, r.Addr) {
			alias := EscapeAddress(origin.String())
			if _, canonical := d.types[typeName][alias]; canonical {
				continue
			}
			if d.typeAliases[typeName] == nil {
				d.typeAliases[typeName] = make(map[string]*declaredEntry)
			}
			if _, taken := d.typeAliases[typeName][alias]; taken {
				continue
			}
			d.typeAliases[typeName][alias] = entry
		}

		escapedBlock := EscapeAddress(r.Addr.ContainingResource().String())
		blk := d.blocks[typeName][escapedBlock]
		if blk == nil {
			blk = &declaredBlock{addr: escapedBlock}
			d.blocks[typeName][escapedBlock] = blk
		}
		blk.instances++
		if r.Addr.Resource.Key != addrs.NoKey {
			blk.keyed = true
		}

		// The same alias, one level up (GitHub issue #198): a marker naming
		// the whole block by a name a moved block says it used to have.
		// Instance-level markers are covered above; this is the pre-instance-key
		// spelling, and without it a for_each block renamed in the same change
		// that gave it for_each would leave its live members as orphans - a
		// proposed destroy - rather than as the "which instance is which"
		// question they actually are.
		for _, origin := range moved.Aliases(movedStmts, r.Addr) {
			aliasBlock := EscapeAddress(origin.ContainingResource().String())
			if _, taken := d.blocks[typeName][aliasBlock]; taken {
				continue
			}
			d.blocks[typeName][aliasBlock] = blk
		}
	}

	d.indexCountBlocks(ctx, req)
	d.aliasMovedCountBlocks(movedStmts)
	return d, diags
}

// aliasMovedCountBlocks files each count block under the block addresses a
// moved block says it used to have, so that a marker naming the old block -
// rather than one of its old instances - still reaches the set it belongs to
// (GitHub issue #198).
//
// The instance aliases in [declaredInstances] cover the ordinary case, where
// the resource had count on both sides of the move and every live member
// carries an indexed marker. They do not cover the block that gained count in
// the same change that renamed it: `moved { from = aws_x.a, to = aws_x.b }`
// where only b has count leaves a live resource carrying the bare marker
// "aws_x.a", which matches no instance alias at all and would fall through to
// being an orphan - a proposed destroy of the very resource the move was
// about. Parking it on the set is what [countBlockFor] already does for a
// marker naming a declared count block by its bare address, and this makes
// the old name one of those. The set matcher then places it by slot, or by
// address for an estate that predates slots, exactly as it places any other
// member.
//
// It runs after indexCountBlocks because cb.entries is what supplies the
// declared instance an origin address is computed from, and it never
// displaces a block address the configuration still declares.
func (d *declared) aliasMovedCountBlocks(stmts []moved.Statement) {
	if len(stmts) == 0 {
		return
	}
	for typeName, blocks := range d.counts {
		names := make([]string, 0, len(blocks))
		for addr := range blocks {
			names = append(names, addr)
		}
		sort.Strings(names)

		for _, addr := range names {
			cb := blocks[addr]
			for _, origin := range moved.Aliases(stmts, cb.instanceAddr(0)) {
				alias := EscapeAddress(origin.ContainingResource().String())
				if _, declared := blocks[alias]; declared {
					continue
				}
				if d.countAliases[typeName] == nil {
					d.countAliases[typeName] = make(map[string]*countBlock)
				}
				if _, taken := d.countAliases[typeName][alias]; taken {
					continue
				}
				d.countAliases[typeName][alias] = cb
				cb.aliases = append(cb.aliases, alias)
			}
		}
	}
}

// countBlockFor finds the count block a marker value names, whether by the
// block's own address or by one a moved block says it used to have. The two
// indexes are deliberately separate: d.counts is the configuration's count
// blocks, one entry each, and everything that enumerates it - the set matcher
// above all - would bind a block once per name it answered to if an alias
// shared that map.
func (d *declared) countBlockFor(typeName, escaped string) *countBlock {
	if cb := countBlockFor(d.counts[typeName], escaped); cb != nil {
		return cb
	}
	return countBlockFor(d.countAliases[typeName], escaped)
}

// indexCountBlocks records the configuration's count blocks and hangs their
// declared instances off them, in index order.
//
// The blocks come from the configuration rather than from the resolutions,
// because a count that has shrunk to zero declares no instances at all and
// still owns whatever is live: `count = var.enabled ? 1 : 0` flipped to false
// is a block with a set of size zero, not a block that stopped existing. Only
// types that something else already put in scope are indexed, since a type
// with no declared instances of any kind is never listed and a count block of
// it would have nothing to match against.
//
// The whole static module tree is walked, not only the root: a resource
// inside a static module may carry count exactly as a root resource can (it
// is the module BLOCK'S count, not a resource's, that RuleChildModule
// refuses permanently). A module reached through a for_each'd module call
// (59c, issue #59 phase 3) is walked once per instance, so a count block
// inside a keyed module is recorded once per module instance too, each
// under its own module-qualified address - "module.app[\"a\"].aws_x.y[2]"
// and "module.app[\"b\"].aws_x.y[2]" are two different count blocks, not
// one. Every block is keyed by its module-qualified address, so two count
// blocks with the same local name in different modules (or different
// instances of the same for_each'd module) never collide. scope is
// [Request.ScopeProvider], threaded through the recursion so a block
// outside this pass's scope (issue #69's multi-provider sweep) never gets
// a count-set entry - or a marker naming one of its slots would be parked
// on a block this pass never declared anything into.
func (d *declared) indexCountBlocks(ctx context.Context, req Request) {
	d.walkCountBlocks(ctx, req.Config, addrs.RootModuleInstance, req.ScopeProvider)

	for typeName, entries := range d.types {
		for _, entry := range entries {
			blockAddr := EscapeAddress(entry.res.Addr.ContainingResource().String())
			cb, ok := d.counts[typeName][blockAddr]
			if !ok {
				continue
			}
			entry.inCount = true
			cb.entries = append(cb.entries, entry)
		}
	}

	// recordBacked entries join the same count blocks, by the same lookup,
	// so a slot is minted or carried for them too - see recordBacked's own
	// doc comment. This is the only thing that reads recordBacked back out:
	// it never joins types, so it plays no part in the scan demand
	// [declared.typeNames] reports or in [declared.entryFor]'s marker
	// lookups.
	for typeName, entries := range d.recordBacked {
		for _, entry := range entries {
			blockAddr := EscapeAddress(entry.res.Addr.ContainingResource().String())
			cb, ok := d.counts[typeName][blockAddr]
			if !ok {
				continue
			}
			entry.inCount = true
			cb.entries = append(cb.entries, entry)
		}
	}

	// Index order, not address order: "aws_eip.pool[10]" sorts before
	// "aws_eip.pool[2]" as a string, and the set matcher pairs the k-th
	// lowest slot with the k-th index.
	for _, blocks := range d.counts {
		for _, cb := range blocks {
			sort.Slice(cb.entries, func(i, j int) bool {
				return instanceIndex(cb.entries[i].res.Addr) < instanceIndex(cb.entries[j].res.Addr)
			})
		}
	}
}

// walkCountBlocks is [declared.indexCountBlocks]'s recursive step: one
// module instance's count blocks, then its children in name order. modInst
// is the instance cfg is being visited as - see [identity.resolver.walkModule]'s
// doc for why it has to be threaded down explicitly rather than recomputed
// from cfg.Path once a for_each module (59c) can be in the tree. scope is
// [Request.ScopeProvider]; inScope compares it against cfg.Path (the
// static module a block is declared in - a provider configuration is a
// static-module fact, unlike modInst, which is a runtime one).
func (d *declared) walkCountBlocks(ctx context.Context, cfg *configs.Config, modInst addrs.ModuleInstance, scope addrs.AbsProviderConfig) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	for _, rc := range cfg.Module.ManagedResources {
		if rc.Count == nil {
			continue
		}
		// A block outside this scope belongs to another pass (#69): it must
		// not get a count-set entry here either, or a marker naming one of
		// its slots would be parked on a block this pass never declared
		// anything into.
		if !inScope(scope, rc, cfg) {
			continue
		}
		typeName := rc.Type
		if d.types[typeName] == nil && d.recordBacked[typeName] == nil {
			// Nothing waiting on discovery for this type at all - not
			// scanned demand, not a recordBacked slot-only entry either -
			// so a count-set entry here would have nothing to match
			// against. See recordBacked's own doc comment for why a type
			// entirely answered from the record still needs this gate to
			// pass: [Request.RecordBackedAddrs] can empty types out
			// completely while still needing its count block indexed for
			// slot purposes.
			continue
		}
		blockAddr := addrs.AbsResource{Module: modInst, Resource: rc.Addr()}.String()
		escaped := EscapeAddress(blockAddr)
		if d.counts[typeName] == nil {
			d.counts[typeName] = make(map[string]*countBlock)
		}
		d.counts[typeName][escaped] = &countBlock{
			addr:     escaped,
			resource: rc.Addr(),
			module:   modInst,
			typeName: typeName,
		}
	}
	for _, name := range identity.SortedChildNames(cfg.Children) {
		child := cfg.Children[name]
		// count first, then for_each, then the unkeyed default - made once,
		// in [identity.ChildCallKeys], because the addresses this loop
		// builds have to be the addresses resolution builds. Reading
		// for_each alone was the bug [stamp.moduleResourcesFrom] had (fixed
		// in de7c0ae3ef) and this walk had after it: a count'd call fell
		// through to ChildModuleKeys with a nil expression, which reports
		// the single unkeyed instance a STATIC call has, so this walk
		// indexed a count'd module's blocks under
		// "module.foo.aws_eip.pool" while resolution and stamping both name
		// them "module.foo[0].aws_eip.pool". Every marker on such a block
		// then missed [declared.countBlockFor], and [countBlock.module] -
		// which is this modInst - carried the unkeyed path into
		// [countBlock.instanceAddr], so a binding that did land named an
		// address no instance has.
		keys, diag := identity.ChildCallKeys(ctx, cfg, name)
		if diag != nil {
			// RuleChildModule already refused this call before discovery
			// ever ran; nothing to index under it.
			continue
		}
		for _, key := range keys {
			d.walkCountBlocks(ctx, child, modInst.Child(name, key), scope)
		}
	}
}

// ---------------------------------------------------------------------------
// Scanning one type
// ---------------------------------------------------------------------------

// scanTypeReporting calls scanType and, when req.Progress is set, reports a
// [ProgressEvent] afterward with the running totals updated in place.
//
// The count comes from res.Scans rather than from scanType's return value,
// because every return path inside scanType (and its Cloud Control cousin,
// [scanTypeCloudControl]) appends exactly one [TypeScan] before returning,
// whether the call resolved into a listed count or refused with a gap - that
// slice is already the one source of truth for "how many resources did this
// type contribute," so reading it back here needs no second bookkeeping path
// that could drift from the first.
func scanTypeReporting(ctx context.Context, req Request, schemas listclient.Schemas, decl *declared, typeName string, res *Result, sweep bool, typesScanned, resourcesFound *int) tfdiags.Diagnostics {
	before := len(res.Scans)
	diags := scanType(ctx, req, schemas, decl, typeName, res, sweep)
	if req.Progress == nil || len(res.Scans) <= before {
		return diags
	}
	for _, scan := range res.Scans[before:] {
		*resourcesFound += scan.Listed
	}
	*typesScanned++
	req.Progress(ProgressEvent{
		TypeName:       typeName,
		Sweep:          sweep,
		TypesScanned:   *typesScanned,
		ResourcesFound: *resourcesFound,
	})
	return diags
}

// scanType lists one resource type and files what comes back.
//
// sweep says this type is being listed because the estate may own resources
// of it that the configuration no longer declares, rather than because
// something declared is waiting to be found. The difference is narrow and
// entirely about what a failure means: a declared type that cannot be listed
// is an error, because the plan would propose creating resources that may
// exist, while a swept type that cannot be listed is a hole in removal
// coverage, reported as a [SweepGap] so that "nothing to destroy" is never
// confused with "nobody looked".
func scanType(ctx context.Context, req Request, schemas listclient.Schemas, decl *declared, typeName string, res *Result, sweep bool) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	scan := TypeScan{
		TypeName: typeName,
		Declared: len(decl.types[typeName]),
		Sweep:    sweep,
		Source:   SourceProvider,
	}

	ts, ok := schemas.Get(typeName)
	if !ok {
		// No native list resource. Before falling to the tag-based Cloud
		// Control leg, see whether issue #272's content-match leg applies:
		// a type in [identity.ContentMatchTypes] carries no tags argument
		// at all, so routing it through [scanTypeCloudControl] would only
		// ever produce [ProblemNoTags] - every listed candidate genuinely
		// has no Tags property to read. The same nil-CloudControl guard
		// applies here as below: a caller that never configured one gets
		// today's refusal unchanged.
		//
		// Gated on NOT already unique-name eligible: [uniqueNameIndexFor]
		// answers the same "is a bare listed name enough to bind this
		// instance" question content-match does, from the ratified row's
		// own [identity.TypeIdentity.UniqueName] rather than a fresh
		// per-instance re-evaluation, and it is the mechanism admission
		// itself already resolved this type onto (Resolution.Cause
		// [identity.DiscoveryUniqueName]). Content-match's own role is
		// rescuing a type the veto would otherwise refuse outright - see
		// [markerless]'s doc comment - not standing in for a binding a
		// ratified row already performs. Both legs currently qualify the
		// same handful of CloudFront/Route53 types (the two-source
		// uniqueness proof that admits a row is the same evidence
		// contentMatchRoster reads), so without this guard content-match
		// would shadow scanUniqueName inside [scanTypeCloudControl] below
		// and that function would never run for them.
		if req.CloudControl != nil {
			if binding, ok := identity.ContentMatchTypes[typeName]; ok {
				if _, byName := uniqueNameIndexFor(decl, typeName); !byName {
					return scanTypeContentMatch(ctx, req, decl, typeName, binding, res, sweep)
				}
			}
		}
		// Before refusing, see whether Cloud Control can enumerate this
		// type instead (issue #47): the mapped CFN type has to exist and be
		// listable with no required input, and a caller has to have
		// configured a Cloud Control client at all - nil Request.CloudControl
		// is "the fallback does not apply here", not an error, so every
		// existing caller that never heard of Cloud Control keeps today's
		// refusal unchanged.
		if cfnType, ccOK := cloudControlSource(req, typeName); ccOK {
			return scanTypeCloudControl(ctx, req, decl, typeName, cfnType, res, sweep)
		}

		// Issue #293. Neither route above found a way to list typeName at
		// all. A declared instance of a taggable type still has one more
		// place to look before this refuses: the estate's tag index issue
		// #266 already fetched, which finds a resource by its own marker
		// and ARN rather than by enumerating its type - see
		// [scanTypeMarkerFallback]'s doc comment for the full reasoning,
		// including why the sweep never takes this branch. Gated on
		// taggability first so an untaggable type - which could never
		// carry a marker for the index to find - reaches the same refusal
		// it always has, unweakened.
		if !sweep && typeTaggable(schemas, typeName) {
			if fbDiags, ok := scanTypeMarkerFallback(ctx, req, decl, typeName, res); ok {
				return diags.Append(fbDiags)
			}
		}

		// [scanTypeMarkerFallback]'s untaggable companion. A type with no
		// tags argument at all could never carry a marker for the index
		// above to find, but untaggable does not mean unidentifiable (see
		// .claude/agents/live-markers.md's invariant): a migration already
		// reads such an object once, for residue (#341), and writes the
		// identity it read into the estate's record store. See
		// [scanTypeLocatedFallback]'s doc comment.
		if !sweep && !typeTaggable(schemas, typeName) {
			if fbDiags, ok := scanTypeLocatedFallback(ctx, req, decl, typeName, res); ok {
				return diags.Append(fbDiags)
			}
		}

		res.Scans = append(res.Scans, scan)
		if sweep {
			return diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapNotListable,
				Detail: fmt.Sprintf(
					"The provider cannot list %s, so this run could not look for resources of that type which this estate owns but no longer declares. A deleted %s block's live resource stays live until the provider can list the type.",
					typeName, typeName),
			}))
		}
		decl.unscanned[typeName] = true
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemTypeNotListable,
			TypeName: typeName,
			Detail: fmt.Sprintf(
				"The provider cannot list %s, so the %d declared instance(s) of it cannot be discovered by marker. The provider needs list support for this type before live resource markers can manage it.",
				typeName, scan.Declared),
		}))
	}

	if sweep && !markerCapable(ts) {
		res.Scans = append(res.Scans, scan)
		return diags.Append(sweepGapDiag(res, SweepGap{
			TypeName: typeName,
			Reason:   SweepGapNotTaggable,
			Detail: fmt.Sprintf(
				"A %s carries no tags, so it can carry no ownership marker and the sweep has nothing to search on. Destroy a resource of this type before removing its block, or delete it out of band.",
				typeName),
		}))
	}

	vals := make(map[string]cty.Value)
	if hasAttr(ts.Config, "region") && req.Region != "" {
		vals["region"] = cty.StringVal(req.Region)
	}

	filterOK, filterWhy := supportsTagFilter(ts)
	switch {
	case req.CollectUnclaimed && !sweep:
		scan.Filtering = FilterClientSide
		scan.Scope = ScopeAll
		scan.FilterReason = "unclaimed resources were asked for, which a server-side estate filter would hide"
	case filterOK:
		// The sweep always takes this branch when the type offers it, which
		// is what makes it cheap: a filtered list of a type this estate holds
		// nothing of comes back empty without the account's whole population
		// crossing the wire.
		scan.Filtering = FilterServerSide
		scan.Scope = ScopeEstate
		vals["filter"] = tagFilter(TagEstate, req.Estate)
	default:
		scan.Filtering = FilterClientSide
		scan.Scope = ScopeAll
		scan.FilterReason = filterWhy
	}

	config, cfgDiags := ts.BuildConfig(vals)
	diags = diags.Append(cfgDiags)
	if cfgDiags.HasErrors() {
		res.Scans = append(res.Scans, scan)
		if sweep {
			return diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapConfigFailed,
				Detail: fmt.Sprintf(
					"The list configuration for %s could not be built from the provider's schema, so the sweep could not look for undeclared resources of that type. This is a bug in the provider's list schema or in the live-markers pipeline, not a fact about the estate.",
					typeName),
			}))
		}
		decl.unscanned[typeName] = true
		return diags
	}

	if scan.Filtering == FilterClientSide {
		log.Printf("[DEBUG] stateless/discovery: listing %s unfiltered (%s)", typeName, scan.FilterReason)
	}

	// The full object is always requested: the markers are resource tags,
	// and a list identity carries only the identity attributes.
	results, listDiags := listclient.List(ctx, req.Provider, typeName, config, true)
	if listDiags.HasErrors() {
		res.Scans = append(res.Scans, scan)
		if sweep {
			// Not appended to diags: a provider that cannot list a type this
			// configuration never mentions must not fail the run, or adding a
			// type to the admission table would break every estate whose
			// provider version does not list it yet. The gap is reported.
			return diags.Append(sweepGapDiag(res, SweepGap{
				TypeName: typeName,
				Reason:   SweepGapListFailed,
				Detail: fmt.Sprintf(
					"Listing %s failed, so the sweep could not look for resources of that type which this estate owns but no longer declares: %s.",
					typeName, listDiags.Err()),
			}))
		}
		decl.unscanned[typeName] = true
		diags = diags.Append(listDiags)
		return diags.Append(problemDiag(res, Problem{
			Kind:     ProblemListFailed,
			TypeName: typeName,
			Detail:   fmt.Sprintf("The provider failed while listing %s, so nothing of that type could be discovered: %s.", typeName, listDiags.Err()),
		}))
	}
	diags = diags.Append(listDiags)
	scan.Listed = len(results)
	if sweep {
		res.SweepCovered = append(res.SweepCovered, typeName)
	}

	var sawAccountID, sawIdentity bool
	// sweepUntaggedReported keeps a per-object gap during a sweep from
	// becoming a SweepGap-per-instance pile-up: the type is what has no
	// coverage, not each individual malformed object of it, so this loop
	// files at most one gap for it, the same "once per type" shape every
	// other SweepGap in this function already has (each of those returns
	// before this loop even starts).
	sweepUntaggedReported := false
	for _, r := range results {
		if acct, ok := r.IdentityAttr("account_id"); ok {
			sawIdentity = true
			if acct != "" {
				sawAccountID = true
				scan.AccountID = acct
			}
		}

		importID, idAttr, hasID := importIdentity(typeName, r)

		tags, taggable := markers.TagsOf(r.Resource)

		// Issue #266: the list call may have dropped this object's tags -
		// iam:ListRoles returns none at all - and an object whose marker
		// cannot be read is one a needs-discovery instance can never bind
		// to, so the plan proposes creating a resource that already exists.
		// The estate's tag index has the tags; join them on by identifier.
		// See bindtags.go for the three gates that keep this from adopting
		// somebody else's object.
		if tags[TagEstate] == "" {
			joined, outcome := req.markers.join(ctx, typeName, importID)
			switch outcome {
			case joinBound:
				tags, taggable = joined, true
				scan.Joined++
				log.Printf("[DEBUG] stateless/discovery: %s %q came back from the list call with no ownership marker; joined one from the estate's tag index", typeName, importID)
			case joinAmbiguous:
				diags = diags.Append(problemDiag(res, Problem{
					Kind:     ProblemAmbiguousTagJoin,
					TypeName: typeName,
					LiveIDs:  liveIDs(importID),
					Detail: fmt.Sprintf(
						"The provider listed a %s (%s) carrying no ownership marker, and more than one resource in estate %q's tag index has that identifier and a tofu-address naming a %s: %s. Nothing in either answer says which is the listed object, so no marker was read off it. Retag or remove the duplicates.",
						typeName, importID, req.Estate, typeName, strings.Join(req.markers.matchedARNs(typeName, importID), ", ")),
				}))
			}
		}
		if tags[TagEstate] == "" && !sweep {
			// The object is one this run could not read a marker off: it
			// carries none, or it carries one the run could not see. Those
			// are the same output, which is why [unreadableMarkerProblem]
			// keys on the count rather than on which types lose tags.
			decl.unreadable[typeName]++
		}

		if !taggable {
			if sweep {
				// The runtime twin of the schema-level check above
				// (SweepGapNotTaggable, "a %s carries no tags"): here the
				// type's schema DOES declare a tags attribute, but this
				// particular listed object came back without one anyway -
				// a provider or emulator quirk on the object, not a fact
				// about the type, which is why it files under its own
				// reason (SweepGapObjectUntagged) rather than the
				// standing-fact one. Either way a sweep is looking for THIS
				// estate's markers among an admitted type the configuration
				// never declares, and an unreadable object there is a hole
				// in removal coverage, not a reason to fail every plan this
				// estate ever runs - the same reasoning SweepGapListFailed
				// already gives a few lines above for "the provider failed
				// while listing". Never taken for a DECLARED type's own
				// scan: an estate that actually declares this type and
				// cannot read its own resource's markers still hard-fails
				// below, unchanged.
				if !sweepUntaggedReported {
					sweepUntaggedReported = true
					diags = diags.Append(sweepGapDiag(res, SweepGap{
						TypeName: typeName,
						Reason:   SweepGapObjectUntagged,
						Detail: fmt.Sprintf(
							"The estate-wide sweep listed a %s with no tags attribute on the returned object, so its ownership markers cannot be read and it cannot be matched to this estate. This is a provider or emulator bug; the sweep continues over the rest of this type's objects and every other type.",
							typeName),
					}))
				}
				continue
			}
			if !markerCapable(ts) {
				// Issue #322: the type's own schema has no tags attribute at
				// all - [markerCapable] read the same way [typeTaggable]
				// reads it, off the provider schema rather than off this one
				// listed object - so no object of this type could EVER carry
				// a marker. That is not a provider bug; it is the same
				// expected shape [markerCapable] already routes gracefully
				// a few lines up for the sweep leg (SweepGapNotTaggable /
				// SweepGapObjectUntagged) and that [uniqueNameIndexFor] /
				// [scanUniqueName] already route around entirely for a
				// statically-named untaggable type. Escalating here to
				// [ProblemNoTags] - an ERROR that aborts the whole plan, see
				// [Severity] - would fail every OTHER resource in the estate
				// over one address this run already reports gracefully: the
				// decl.unreadable increment above feeds
				// [unreadableMarkerProblem]'s per-address WARNING at bind
				// time, which is the correct and sufficient diagnostic for a
				// resource that structurally cannot carry a marker.
				//
				// This does not weaken the genuine anomaly one branch up:
				// when the schema DOES declare tags (markerCapable true) but
				// this object came back without them anyway, taggable is
				// still false and the ProblemNoTags error below still fires,
				// unchanged - that shape is still a real provider or emulator
				// bug worth aborting over, not this one.
				continue
			}
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemNoTags,
				TypeName: typeName,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"The provider listed a %s with no tags attribute on the returned object, so its ownership markers cannot be read. This is a provider bug or a type that should not be marker-discoverable.",
					typeName),
			}))
			continue
		}

		estate := tags[TagEstate]
		switch {
		case estate == "":
			if sweep {
				// A sweep is looking for this estate's markers and for
				// nothing else. The type is not in the configuration, so no
				// declared instance could ever be offered this resource for
				// adoption, and reporting it as foreign would widen the
				// foreign section to every admitted type in the account.
				continue
			}
			scan.Unclaimed++
			res.Unclaimed = append(res.Unclaimed, UnclaimedResource{
				TypeName:     typeName,
				ImportID:     importID,
				IdentityAttr: idAttr,
				Identity:     r.Identity,
				DisplayName:  r.DisplayName,
				Tags:         tags,
				Resource:     r.Resource,
			})
			continue
		case estate != req.Estate:
			// Another estate's resource. Not ours, not foreign, not
			// reported: ignored entirely, which is the whole point of the
			// estate being the ownership boundary.
			scan.OtherEstate++
			continue
		}

		raw, corrupt := markers.GatherAddress(tags)
		if corrupt {
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemMalformedMarker,
				TypeName: typeName,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"A live %s claims estate %q but its tofu-address continuation tags have a gap in them - one of tofu-address-2, tofu-address-3, ... is missing while a later one is present. Per live/MARKERS.md such a resource is malformed - neither owned nor foreign - and a human has to say which address it belongs to; discovery will not guess.",
					typeName, req.Estate),
			}))
			continue
		}
		escaped := EscapeAddress(raw)
		if !ValidMarkerAddress(escaped) {
			what := "carries no tofu-address tag"
			if raw != "" {
				what = fmt.Sprintf("carries the tofu-address value %q, which is not a well-formed escaped address", raw)
			}
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemMalformedMarker,
				TypeName: typeName,
				Marker:   raw,
				LiveIDs:  liveIDs(importID),
				Detail: fmt.Sprintf(
					"A live %s claims estate %q but %s. Per live/MARKERS.md such a resource is malformed - neither owned nor foreign - and a human has to say which address it belongs to; discovery will not guess.",
					typeName, req.Estate, what),
			}))
			continue
		}

		// bindType is the type every declared-set lookup and reported record
		// below uses to find where this object belongs. It starts as
		// typeName - which list call found the object - and is corrected to
		// the marker's own type only for the cases known safe: see
		// defaultAdopterSiblings and iamServiceLinkedRoleSibling.
		bindType := typeName
		// claimIdentity is the identity object the claimant carries;
		// ordinarily r.Identity unchanged, and only ever overridden in the
		// identity-recomposing branch below, where r.Identity's schema no
		// longer matches bindType.
		claimIdentity := r.Identity
		if markerType := markerTypeOf(escaped); markerType != typeName {
			// Both predicates name the same shape: AWS itself has no
			// separate list call for the special case, so one type's native
			// list call returns objects a second registered type manages.
			// #305/#325's adopt-don't-create family (a route table is a
			// route table, and DescribeRouteTables returns the VPC's
			// default one right alongside every other) and #302's
			// role/service-linked-role overlap (IAM has no
			// ListServiceLinkedRoles, so iam:ListRoles returns both) are
			// the two known instances. In neither is this a cross-type
			// marker: it is the same live object this list call was always
			// going to return, and the marker's own type - not the list
			// call that happened to surface it - says which declared
			// instance it is.
			overlappingListCall := defaultAdopterSiblings(markerType, typeName) ||
				iamServiceLinkedRoleSibling(markerType, typeName)
			switch {
			case overlappingListCall && sameRatifiedIdentity(markerType, typeName):
				// The two names agree about what this type's import
				// identity IS, so the identity [importIdentity] already
				// read under typeName's row is bindType's identity too and
				// carries forward unchanged. aws_default_security_group /
				// aws_security_group and aws_default_network_acl /
				// aws_network_acl are this case: both sides of each pair
				// import by the object's own id.
				bindType = markerType
			case overlappingListCall:
				// The two names do NOT agree, so a bindType flip alone
				// would carry the wrong importID forward, silently. Two
				// real instances, one per predicate:
				//
				//   - aws_iam_role imports by bare role name, while
				//     aws_iam_service_linked_role's documented import ID is
				//     the role's ARN (issue #302; see tagging.go's
				//     iamRoleEntry doc comment).
				//   - aws_route_table imports by the route table's own
				//     rtb-… id, while aws_default_route_table imports by
				//     the VPC's id - the provider's own Import section says
				//     so and means it literally, and a route table id gets
				//     "Error: empty result" from the real provider (issue
				//     #332).
				//
				// [importIdentityFromResource] recomposes it from this same
				// listed object's own attributes, under bindType's own
				// ratified identity attribute rather than typeName's: the
				// listed object is the same live object either way, so it
				// carries every attribute either schema exports.
				if fixedID, fixedAttr, composedOK := importIdentityFromResource(markerType, r.Resource); composedOK {
					bindType = markerType
					importID, idAttr, hasID = fixedID, fixedAttr, true
					// r.Identity is typed by typeName's identity schema,
					// not bindType's - carrying it forward under the
					// corrected type would hand a wrongly-shaped object to
					// whatever reads Binding.Identity downstream. Every
					// other claimant construction that composes an identity
					// by hand rather than trusting the list call's own
					// schema-matched identity (tagging.go, cloudcontrol.go)
					// already uses cty.NilVal for the same reason.
					claimIdentity = cty.NilVal
				} else {
					diags = diags.Append(problemDiag(res, Problem{
						Kind:     ProblemMalformedMarker,
						TypeName: typeName,
						Marker:   raw,
						LiveIDs:  liveIDs(importID),
						Detail: fmt.Sprintf(
							"A live %s claims estate %q and carries the tofu-address value %q, which names a %s rather than a %s. A marker names the resource it is written on (see live/MARKERS.md). This looks like the overlapping-list-call case issues #302 and #332 describe - the two types share one live object - but %s imports by a different identity than %s does and %s could not be read off the listed object, so it was not corrected automatically. Retag the resource with its own address, or remove the marker to disown it.",
							typeName, req.Estate, raw, markerType, typeName, markerType, typeName, identityAttrNames(markerType)),
					}))
					continue
				}
			default:
				// The estate owns this resource and its marker names an
				// address of another, unrelated type. Nothing can be done
				// with it: binding it to the address it names would attach a
				// plan for one resource type to a resource of another, and
				// ignoring it would leave a resource this estate owns
				// invisible to every section of the output. So it is the
				// marker spec's third answer - malformed - and a human says
				// which address it belongs to. (Audit finding C4: this used
				// to match the declared-address set, which carried no type,
				// and the resource was silently dropped.)
				diags = diags.Append(problemDiag(res, Problem{
					Kind:     ProblemMalformedMarker,
					TypeName: typeName,
					Marker:   raw,
					LiveIDs:  liveIDs(importID),
					Detail: fmt.Sprintf(
						"A live %s claims estate %q and carries the tofu-address value %q, which names a %s rather than a %s. A marker names the resource it is written on (see live/MARKERS.md). Retag the resource with its own address, or remove the marker to disown it.",
						typeName, req.Estate, raw, markerType, typeName),
				}))
				continue
			}
		}

		c := claimant{
			importID:     importID,
			identityAttr: idAttr,
			identity:     claimIdentity,
			displayName:  r.DisplayName,
			marker:       raw,
			escaped:      escaped,
			normalized:   escaped != raw,
			slot:         tags[TagSlot],
			tags:         tags,
			noIdentity:   !hasID,
		}

		if entry, ok := decl.entryFor(bindType, escaped); ok {
			if !claimantAlreadyPresent(entry.claimants, c) {
				entry.claimants = append(entry.claimants, c)
			}
			continue
		}
		if decl.declares(bindType, escaped) {
			// A declared instance whose identity came out of the
			// configuration rather than out of a marker: nothing was waiting
			// to be found here, and the projection reads it by the identity
			// the configuration gives. What is left for this pass is the
			// half of the ownership question the projection cannot see -
			// whether this object is the instance that address names, or a
			// second object left carrying its marker (GitHub issue #244).
			// Reported, never acted on: see displaced.go.
			if want, displaced := decl.displacedFrom(bindType, escaped, c); displaced {
				diags = diags.Append(problemDiag(res, displacedProblem(req, bindType, escaped, want, c)))
			}
			continue
		}
		// A marker naming a count block, by its bare address or by an index
		// the configuration no longer expands to. It is not an orphan and
		// not a stranger: it is a member of that block's set whose position
		// in the set its address does not settle, which is exactly the
		// question slots answer. Parking it on the block hands it to the set
		// matcher, which either binds it by slot or - for an estate with no
		// slots - puts it back where it was.
		if cb := decl.countBlockFor(bindType, escaped); cb != nil {
			cb.extra = append(cb.extra, c)
			continue
		}
		if blk, ok := decl.blocks[bindType][escaped]; ok && blk.keyed {
			// The marker names the resource block, not one of its
			// instances: markers written before instance keys were part of
			// the address. For a for_each block nothing distinguishes which
			// live resource is which declared instance, and the address is
			// the only identity a for_each instance has.
			blk.claimants = append(blk.claimants, c)
			continue
		}
		res.Orphans = append(res.Orphans, OwnedResource{
			TypeName:     bindType,
			ImportID:     importID,
			IdentityAttr: idAttr,
			Identity:     r.Identity,
			Marker:       raw,
			Normalized:   escaped,
			Slot:         tags[TagSlot],
			DisplayName:  r.DisplayName,
			Tags:         tags,
			Resource:     r.Resource,
			Swept:        sweep,
		})
	}

	if scan.Filtering == FilterServerSide && sawIdentity && !sawAccountID && scan.Listed > 0 {
		diags = diags.Append(problemDiag(res, Problem{
			Kind:     ProblemUnresolvedAccount,
			TypeName: typeName,
			Detail: fmt.Sprintf(
				"Every %s the provider listed came back with an empty account ID, so the owner-id filter the provider appends to a filtered list went out empty and real EC2 would silently match nothing. Remove skip_requesting_account_id from the provider configuration so the provider can resolve the account via STS.",
				typeName),
		}))
	}

	res.Scans = append(res.Scans, scan)
	return diags
}

// markerTypeOf is the resource type a marker value names.
//
// [UnescapeAddress] is the real answer whenever it has one: it validates the
// whole shape, not just where the type segment sits, so
// "aws.default.aws_vpc.main" - a would-be module prefix whose first segment
// is not literally "module" - is rejected rather than read as a two-segment
// address with an aws_vpc type by coincidence of position. When it cannot
// decode the value at all (an out-of-set instance key, most commonly), the
// first segment is still the best available guess, and everything this is
// used for treats "does not equal the live object's real type" as the
// signal, so a guess that undershoots costs nothing: a value this fallback
// cannot parse was never going to bind to anything anyway.
func markerTypeOf(escaped string) string {
	if addr, ok := UnescapeAddress(escaped); ok {
		return addr.Resource.Resource.Type
	}
	head, _, _ := strings.Cut(escaped, ".")
	return head
}

// defaultAdopterPrefix names the AWS provider's "adopt the account or VPC's
// already-existing default object rather than creating one" family:
// aws_default_vpc, aws_default_subnet, aws_default_vpc_dhcp_options,
// aws_default_route_table, aws_default_security_group and
// aws_default_network_acl are the whole documented set (every one of their
// doc pages states, verbatim, "Terraform does not _create_ this resource but
// instead attempts to \"adopt\" it into management"). Only the last three are
// admitted into [identity.DefaultTable] today (#305); [defaultAdopterSiblings]
// derives the relationship from this prefix and the ratified table rather
// than a hand list of three pairs, so admitting aws_default_vpc or
// aws_default_subnet later needs no change here to be recognized too.
const defaultAdopterPrefix = "aws_default_"

// defaultAdopterSiblings reports whether a and b are the two admitted names
// of one adopt-don't-create pair: the plain type AWS mints exactly one of per
// VPC or account, and the aws_default_* type that manages that same live
// object under a second registered name.
//
// What it proves is that the two names denote ONE live object, which is what
// [scanType] needs before it will let a marker of one type bind an object the
// other type's list call surfaced: the name relationship is exact rather than
// a prefix guess (aws_default_X pairs only with aws_X, and neither side may
// itself be a default adopter of the other), both names are admitted, and both
// are ratified server-assigned - which is what makes the object AWS itself
// minted the one either name manages.
//
// It deliberately does NOT require the two rows to agree about what that
// object's import identity is. It used to, and that equality read as a safety
// proof while actually concealing a defect: aws_default_route_table's ratified
// row claimed the route table's own rtb-… id, matching aws_route_table's, and
// the real provider answers "Error: empty result" for it - the documented and
// verified import identity is the VPC's id (issue #332). "The same object" and
// "the same import identity" are two facts, so they are checked separately
// now: [sameRatifiedIdentity] decides whether the identity already read under
// the listing type's row carries forward unchanged or has to be recomposed
// under the marker type's own row by [importIdentityFromResource]. A pair whose
// identities diverge and whose marker type's identity attribute cannot be read
// off the listed object still refuses as a malformed marker; nothing is
// guessed.
//
// This is [scanType]'s only caller, which is why the check runs on demand
// rather than as a table precomputed at package init: the admitted set is
// small (three pairs today) and the lookups are two map reads.
func defaultAdopterSiblings(a, b string) bool {
	plain, def := a, b
	if strings.HasPrefix(a, defaultAdopterPrefix) {
		plain, def = b, a
	}
	if !strings.HasPrefix(def, defaultAdopterPrefix) || strings.HasPrefix(plain, defaultAdopterPrefix) {
		return false
	}
	if defaultAdopterPrefix+strings.TrimPrefix(plain, "aws_") != def {
		return false
	}
	plainTI, ok := identity.LookupType(plain)
	if !ok {
		return false
	}
	defTI, ok := identity.LookupType(def)
	if !ok {
		return false
	}
	return plainTI.ServerAssigned && defTI.ServerAssigned
}

// defaultAdopterPlainSibling names the plain type an aws_default_* adopter
// shares its one live object with - aws_route_table for
// aws_default_route_table - or reports false when typeName is not an admitted
// adopter with an admitted sibling.
//
// It is [defaultAdopterSiblings] asked from one side instead of two, for the
// callers that hold one type name and need the other rather than a yes/no; the
// pairing rule and its proof live there.
func defaultAdopterPlainSibling(typeName string) (string, bool) {
	if !strings.HasPrefix(typeName, defaultAdopterPrefix) {
		return "", false
	}
	plain := "aws_" + strings.TrimPrefix(typeName, defaultAdopterPrefix)
	if !defaultAdopterSiblings(typeName, plain) {
		return "", false
	}
	return plain, true
}

// sameRatifiedIdentity reports whether two admitted types' ratified rows
// describe the same import identity: the same documented syntax, and the same
// identity attributes to read it out of.
//
// [scanType] uses it to pick which of two treatments an overlapping-list-call
// sibling pair gets. True means the import identity [importIdentity] already
// read under the listing type's row IS the marker type's identity too, so it
// carries forward untouched - aws_default_security_group / aws_security_group
// and aws_default_network_acl / aws_network_acl, where both sides import by the
// object's own id. False means it is not, and has to be recomposed from the
// listed object under the marker type's own row - aws_default_route_table /
// aws_route_table (issue #332) and aws_iam_service_linked_role / aws_iam_role
// (issue #302).
//
// An unadmitted type answers false: the absence of a row is not agreement, and
// the caller's recomposition path refuses when it cannot read an identity
// rather than carrying one forward on faith.
func sameRatifiedIdentity(a, b string) bool {
	aTI, ok := identity.LookupType(a)
	if !ok {
		return false
	}
	bTI, ok := identity.LookupType(b)
	if !ok {
		return false
	}
	return aTI.ImportSyntax == bTI.ImportSyntax && slices.Equal(aTI.IdentityAttrs, bTI.IdentityAttrs)
}

// iamServiceLinkedRoleSibling reports whether a and b are aws_iam_role and
// aws_iam_service_linked_role, in either order - GitHub issue #302.
//
// It is the same "AWS itself has no separate list call for the special
// case" shape [defaultAdopterSiblings] names above: IAM has no
// ListServiceLinkedRoles operation, so iam:ListRoles - aws_iam_role's own
// native list call - returns every service-linked role right alongside the
// ordinary ones, no PathPrefix filter applied. That is what #302 pointed at
// [defaultAdopterSiblings] as precedent for.
//
// It is deliberately not folded into [defaultAdopterSiblings] itself: that
// function's safety proof is "same ImportSyntax, same IdentityAttrs", and
// this pair fails it for real - aws_iam_role imports by bare role name,
// while aws_iam_service_linked_role's documented import ID is the role's
// ARN (tagging.go's iamRoleEntry doc comment carries the same fact,
// confirmed against a live floci-created role while crossing issue #293's
// corpus). A bindType flip using typeName's own importID unchanged would
// carry the wrong value forward, silently. [scanType]'s caller pairs a
// match here with [importIdentityFromResource] to recompose the identity
// under bindType's own scheme instead of reusing typeName's.
func iamServiceLinkedRoleSibling(a, b string) bool {
	return (a == iamRoleTypeName && b == iamServiceLinkedRoleTypeName) || (a == iamServiceLinkedRoleTypeName && b == iamRoleTypeName)
}

// iamRoleTypeName and iamServiceLinkedRoleTypeName are
// [iamServiceLinkedRoleSibling]'s one admitted pair, named once so
// [typeNeedsResourceObjectToRecompose] can ask "is typeName either side of
// it" by identifier rather than retyping the two literals a second time -
// the derivation guard (live/derivation_guard_test.go) counts a literal
// occurrence, not a call site, so a second hand-typed copy would read as a
// second hand-wired surface for the same one fact this const pair already
// carries a reason for.
const (
	iamRoleTypeName              = "aws_iam_role"
	iamServiceLinkedRoleTypeName = "aws_iam_service_linked_role"
)

// importIdentityFromResource composes bindType's own import identity from a
// listed object's full resource attributes, for the overlapping-list-call
// cases where the importID [importIdentity] already composed under the listing
// type's row is not bindType's importID and cannot simply be carried forward
// (see [sameRatifiedIdentity] for which pairs those are).
//
// Which attribute to read is the ratified table's answer, not this function's:
// [identity.TypeIdentity.IdentityAttrs] is defined as "the attribute names
// whose value equals this type's identity", and the leading one is the type's
// own documented import identity. So aws_iam_service_linked_role (IdentityAttrs
// leading with "arn", ImportSyntax "ARN") reads arn, and
// aws_default_route_table (IdentityAttrs ["vpc_id"], ImportSyntax "vpc-ID")
// reads vpc_id. Neither type name appears here; adding a third such pair needs
// only its row to be right.
//
// Reading it off the LISTED object rather than off bindType's own list call is
// the whole point: there is no separate list call, the object the sibling's
// call returned is the same live object, and it carries every attribute either
// schema exports - aws_iam_role's schema exports arn ("Amazon Resource Name
// (ARN) specifying the role.", per iam_role.html.markdown) and
// aws_route_table's exports vpc_id ("The VPC ID.", per
// route_table.html.markdown) regardless of which declared type asked.
//
// Only the LEADING identity attribute is tried. A row listing several
// (["arn", "id"], say) means each of them equals the identity for the type's
// OWN reads; it does not license falling back to a second attribute of a
// sibling's object when the first is missing, which is how a service-linked
// role would come back identified by its bare name instead of its ARN. An
// unreadable leading attribute returns false rather than guessing, and the
// caller's existing malformed-marker refusal stands.
//
// sweepBindType is [scanType]'s markerType/typeName correction
// (defaultAdopterSiblings, iamServiceLinkedRoleSibling, sameRatifiedIdentity),
// asked from a whole-estate sweep instead: [fileTaggingCandidate] (the ARN-
// join tag sweep, issue #51) and [scanTypeCloudControl]'s own sweep leg both
// hit the identical "one AWS list call, two registered types" shape
// scanType's own per-type list call does (issue #394), but neither carries
// the listed object's schema-typed attributes the way scanType's own
// [Resource.Resource] does - only the joined ARN's own importID and the
// object's tags. So this can only ever carry markerType's identity forward
// UNCHANGED ([sameRatifiedIdentity] true); it can never recompose a
// different one the way scanType's own [importIdentityFromResource] does,
// because there is no resource object here to read a second attribute off
// of.
//
// It returns:
//
//   - (typeName, false) when markerType == typeName (nothing to correct), or
//     the pair is not a recognized companion at all: a genuine cross-type
//     marker, which the caller reports as malformed exactly as before this
//     fix. This is also the answer for a recognized companion pair whose
//     ratified rows disagree about the import identity - the route table
//     family, issue #332: recomposing vpc_id needs the listed object's own
//     attributes, which this sweep never has, and guessing would risk
//     exactly the wrong marker HANDOFF's safety rule forbids, so it falls
//     back to the same refusal a genuinely unrelated type gets rather than
//     inventing a third outcome.
//
//   - ("", true) when markerType is itself declared ANYWHERE in the whole
//     configuration ([declared.declares], never [declared.entryFor]): the
//     two disagree exactly for a companion pair split across
//     [Request.ScopeProvider] passes (issue #69's multi-provider sweep,
//     GitHub issue #396), and declares is the one [statelessDiscover]'s own
//     doc comment already promises callers - "Request.ScopeProvider is what
//     keeps a pass from *binding* through the wrong account while still
//     letting it recognize (via declared.declares, built from every
//     resolution regardless of provider) that such an object is somebody
//     else's declared, owned resource rather than an orphan to remove."
//     entryFor reads [declared.types], which [declaredInstances] leaves
//     UNPOPULATED for a resolution outside the CURRENT pass's own
//     [inScope] - correctly, since that map is what drives THIS pass's own
//     binding attempts, not what a companion-pair sighting from another
//     pass should consult. declares reads [declared.all], populated
//     unconditionally, before any scope filtering, straight from
//     [Request.Resolutions] - which is what a same-scope-but-not-yet-
//     scanned companion (this comment's own original scenario: [Discover]
//     always runs the config-driven scan over every declared type before
//     either sweep runs, so markerType's OWN list call - the only place a
//     mismatched-identity pair's recomposed attribute is ever read - has
//     already visited this exact live object under its own name and filed
//     the correct claim there) and a cross-scope companion (a second
//     provider-scoped pass's own sweep re-visiting an object a DIFFERENT
//     pass already bound correctly) both need: is markerType declared at
//     all, not "did THIS call's own scan reach it". This sighting is the
//     ARN join's (or Cloud Control's) generic, wire-shape answer finding
//     the SAME live object a second time, not a second object; the caller
//     skips it rather than filing a second, differently-identified claim for
//     one address. See TestDiscoverDefaultAdopterDeclaredBothSidesNoFalseCollision
//     for the analogous shape at the scanType level, where two DECLARED
//     sides of one pair produce two claimants for one entry rather than two
//     entries, and TestSweepBindTypeSkipsAcrossProviderScopes for the
//     cross-scope shape this fixes.
//
//   - (markerType, false) when the pair's ratified rows agree about the
//     import identity ([sameRatifiedIdentity] true - aws_default_security_group/
//     aws_security_group and aws_default_network_acl/aws_network_acl): the
//     candidate's own importID, already read under typeName's ARN shape or
//     Cloud Control identifier, IS markerType's identity too, so it carries
//     forward unchanged and the caller files the claimant/orphan under
//     markerType.
//
// typeNeedsResourceObjectToRecompose reports whether typeName is one side
// of an admitted companion pair ([defaultAdopterSiblings] or
// [iamServiceLinkedRoleSibling]) whose ratified rows disagree about the
// import identity ([sameRatifiedIdentity] false) - aws_route_table/
// aws_default_route_table (issue #332) and aws_iam_role/
// aws_iam_service_linked_role (issue #302) today.
//
// Binding the shared object under such a pair needs
// [importIdentityFromResource] to recompose the OTHER side's identity from
// the LISTED object's own schema attributes (vpc_id, arn) - which only a
// native per-type list call ([scanType], via [listclient.List]'s
// IncludeResource) ever provides. The estate-wide tag sweep
// ([fileTaggingCandidate], issue #51's GetResources join) carries only the
// joined ARN's own importID and the object's tags, never its schema
// attributes, so [partitionSweepTypes] routes a type this answers true for
// through the native per-type sweep instead, even when
// [Request.TaggingSweep] is set - see its own doc comment.
func typeNeedsResourceObjectToRecompose(typeName string) bool {
	if plain, ok := defaultAdopterPlainSibling(typeName); ok {
		return !sameRatifiedIdentity(typeName, plain)
	}
	if def := defaultAdopterPrefix + strings.TrimPrefix(typeName, "aws_"); defaultAdopterSiblings(typeName, def) {
		return !sameRatifiedIdentity(typeName, def)
	}
	if typeName == iamRoleTypeName || typeName == iamServiceLinkedRoleTypeName {
		return true
	}
	return false
}

// partitionSweepTypes splits [sweepTypes]' universe in two for
// [Request.TaggingSweep] (issue #394): tagging is swept the one-round-trip
// way [sweepViaTagging] exists for, and native is swept the older, one-
// list-call-per-type way ([scanTypeReporting]) for two independent reasons -
// [typeNeedsResourceObjectToRecompose] says a candidate the tag sweep would
// produce for it can never carry enough to bind its companion pair safely,
// or the type has no [arnJoinTable] row to join an ARN through at all
// (found via corpus-rds-complete-postgres's day2_remove unit: aws_db_instance
// has never had one, and 845e7a0d9d's CHOUDOUFU_NODE_RESOLVE default flip
// made it reachable - once every one of an estate's own declared
// aws_db_instance instances is record-backed, [declared.typeNames]'s
// config-driven scan stops covering the type too, and a live orphan of it
// becomes invisible to BOTH legs at once: [sweepViaTagging] reported
// [SweepGapNoARNJoin] and moved on with no fallback, so a block deletion's
// own live object was silently never destroyed and never even diagnosed).
// Every other caller of [sweepTypes] (the non-tagging, "guided" sweep leg)
// already sweeps every type the native way, so it has no use for this split.
//
// This reaches every admitted, cloud-observable type outside
// [arnJoinTable]'s coverage, not aws_db_instance alone - measured against
// live/registry.json's roster, [arnJoinTable] only ever joins CFN types for
// thirteen services (iam, s3, sns, ec2, kms, route53, acm, states, logs,
// dynamodb, ecs, cloudwatch, lambda, elasticloadbalancing); every admitted
// type outside them (rds, autoscaling, sqs, dynamodb's own streams,
// cloudfront, and the rest) was taking this same silent-gap path whenever
// its own config-driven demand happened to be fully record-backed, and now
// falls back to the native list call [scanType] already knows how to make
// for it instead.
func partitionSweepTypes(req Request, decl *declared) (tagging, native []string) {
	for _, t := range sweepTypes(req, decl) {
		if typeNeedsResourceObjectToRecompose(t) || !arnJoinReaches(req, t) {
			native = append(native, t)
		} else {
			tagging = append(tagging, t)
		}
	}
	return tagging, native
}

func sweepBindType(decl *declared, markerType, typeName, escaped string) (bindType string, skip bool) {
	if markerType == typeName {
		return typeName, false
	}
	if !defaultAdopterSiblings(markerType, typeName) && !iamServiceLinkedRoleSibling(markerType, typeName) {
		return typeName, false
	}
	if decl.declares(markerType, escaped) {
		return "", true
	}
	if sameRatifiedIdentity(markerType, typeName) {
		return markerType, false
	}
	return typeName, false
}

// An arn-valued identity goes through [importIDFromARN] rather than being used
// raw, because for some types the documented import ID is the ARN's resource-id
// segment rather than the whole string - that function owns the distinction
// (see [importsWholeARNString]), and tagging.go's ARN-join path already trusts
// it for the identical fact. Every other attribute's value IS the import ID.
func importIdentityFromResource(bindType string, resource cty.Value) (importID, identityAttr string, ok bool) {
	ti, tableOK := identity.LookupType(bindType)
	if !tableOK || len(ti.IdentityAttrs) == 0 {
		return "", "", false
	}
	if resource == cty.NilVal || resource.IsNull() {
		return "", "", false
	}
	attr := ti.IdentityAttrs[0]
	ty := resource.Type()
	if !ty.IsObjectType() || !ty.HasAttribute(attr) {
		return "", "", false
	}
	v := resource.GetAttr(attr)
	// IsMarked checked first, and nothing below reads v.AsString() until
	// this guard has already returned on a marked value - cty panics rather
	// than errors on a marked receiver, and a resource's attribute flowing
	// from a sensitive input variable is the ordinary way to produce one,
	// however unlikely for an identity attribute in practice. See
	// internal/live/marksafe.
	if v.IsMarked() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String {
		return "", "", false
	}
	if v.AsString() == "" {
		return "", "", false
	}
	if attr == "arn" {
		return importIDFromARN(ti, v.AsString())
	}
	return v.AsString(), attr, true
}

// claimantAlreadyPresent reports whether cs already holds a claimant for the
// same live object as c, by import ID. An estate that declares BOTH sides of
// a [defaultAdopterSiblings] pair - a real, ordinary shape: a VPC module's
// aws_default_security_group.this sitting next to an unrelated
// aws_security_group.other block - has decl.typeNames() include both types,
// so [scanType] runs once per type and each run's own list call
// (DescribeSecurityGroups, DescribeRouteTables, DescribeNetworkAcls) returns
// every security group / route table / network ACL in the account,
// including the shared default object. That object's marker settles its
// bindType correctly every time (see the defaultAdopterSiblings branch
// above), so both scans append an otherwise-identical claimant - same
// importID, because it is the same live resource - for the very entry this
// function guards. Without this check that reads as
// [ProblemCollision] ("Two live resources claiming one address") printing
// the same ID twice, rather than the single object it actually is. A
// claimant with no importID (noIdentity) is never deduplicated against
// anything: "no identity" is not an identity two claimants could share.
func claimantAlreadyPresent(cs []claimant, c claimant) bool {
	if c.importID == "" {
		return false
	}
	for _, existing := range cs {
		if existing.importID == c.importID {
			return true
		}
	}
	return false
}

// typeTaggable reports whether typeName's own managed resource schema has a
// settable tags map at all - [markers.Taggable], the same predicate
// live/survey-full.json's signals.taggable column and stamping itself use,
// read via [listclient.Schemas.ResourceSchema] rather than [Schemas.Get] so
// a type with no list route still gets an answer. It is what
// [scanTypeMarkerFallback] (issue #293) gates on before ever asking the tag
// index about a type that could never have carried a marker in the first
// place - a false here is not "the index found nothing", it is "there was
// never anything for the index to find", and the caller's existing refusal
// must stand unweakened.
func typeTaggable(schemas listclient.Schemas, typeName string) bool {
	block, ok := schemas.ResourceSchema(typeName)
	if !ok {
		return false
	}
	return markers.Taggable(block)
}

// markerCapable reports whether a resource type can carry the ownership
// markers at all, read from the provider's own schema for the type rather
// than from a list in stateless mode: a type with no tags attribute has nowhere
// to put a tofu-estate tag, so no sweep of it could ever find anything.
func markerCapable(ts listclient.TypeSchema) bool {
	if ts.Resource == nil {
		return false
	}
	return hasAttr(ts.Resource, "tags") || hasAttr(ts.Resource, "tags_all")
}

// ---------------------------------------------------------------------------
// Orphans
// ---------------------------------------------------------------------------

// classifyOrphans decides which owned-but-undeclared resources this run
// proposes destroying, and puts the ones it does into the resolution list.
//
// The order of the checks is the whole safety property, and it is this way
// round on purpose. A rename is a resource that needs a new tag; a removal is
// a resource that needs to stop existing. Getting that wrong in the safe
// direction costs an operator one command; getting it wrong in the other
// direction destroys and recreates a resource that nobody asked to touch. So
// an orphan sitting in a resource block that still has an unclaimed declared
// instance is withheld from removal before anything else is considered -
// before the pairing rule in the foreign package has even run, and whether or
// not that rule will end up offering a command. The classifier's job is to
// name the pairing; withholding is not conditional on it succeeding, because
// the case where it fails - two orphans and two unclaimed instances, say - is
// the case where guessing is least defensible.
//
// [blockKey] is what "the same resource block" means for that first check,
// and issue #316 is what happens when the two sides of it disagree: the
// declared side dropped the module path while the read side cut the escaped
// marker at its first ":", which in a module-qualified marker is the module
// step's own key rather than the resource's. The two strings could only ever
// be equal for a root-module address, so the guard fired for a re-keyed root
// resource and for nothing inside any module at all - and a for_each key
// renamed inside an ordinary static module was destroyed and recreated,
// which is the exact outcome the paragraph above says must not happen.
//
// What survives that check reaches the projection as a concrete resolution
// with no configuration behind it, which is precisely the shape a stock run's
// prior state has for a resource whose block was deleted, and which the plan
// engine's own orphan handling turns into a destroy.
func classifyOrphans(req Request, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	if len(res.Orphans) == 0 {
		return diags
	}

	// The resource blocks that still have a declared instance nothing
	// claimed. Membership here is what makes an orphan a possible rename.
	pending := make(map[string]bool, len(res.Unbound))
	for _, addr := range res.Unbound {
		pending[blockKey(addr)] = true
	}

	// Two live resources whose markers unescape to one address would
	// overwrite each other in the prior state, and the survivor would be
	// destroyed while the other stayed live and unrecorded.
	byAddr := make(map[string][]int)
	for i := range res.Orphans {
		o := &res.Orphans[i]
		o.Addr, o.Addressable = UnescapeAddress(o.Normalized)
		if o.Addressable {
			byAddr[o.Addr.String()] = append(byAddr[o.Addr.String()], i)
		}
	}

	for i := range res.Orphans {
		o := &res.Orphans[i]
		block := orphanBlockKey(o)

		switch {
		case pending[block]:
			o.Withheld = fmt.Sprintf(
				"a declared instance of %s is unclaimed, so this may be the same resource under a new instance key rather than a resource to destroy; see the rename section",
				block)
		case !o.Addressable:
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemMalformedMarker,
				TypeName: o.TypeName,
				Marker:   o.Normalized,
				LiveIDs:  liveIDs(o.ImportID),
				Detail: fmt.Sprintf(
					"A live %s carries estate %q and the tofu-address value %q, which no declared instance matches and which the escaping rule in live/MARKERS.md cannot turn back into an address. Nothing is proposed for it: give it a marker that round-trips, or remove it.",
					o.TypeName, req.Estate, o.Normalized),
			}))
			o.Withheld = "its marker cannot be turned back into an address, so there is no instance to plan a destroy at"
		case o.Addr.Resource.Resource.Type != o.TypeName:
			// The marker round-trips into an address of another type. The scan
			// refuses these at the source, so reaching here means a caller
			// assembled the orphan list itself; either way the answer is the
			// same and it is never a destroy, because the address a removal
			// would be planned at names a different resource than the live one
			// it would destroy (audit finding C4, the A1 illegibility).
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemMalformedMarker,
				TypeName: o.TypeName,
				Addr:     o.Addr,
				Marker:   o.Normalized,
				LiveIDs:  liveIDs(o.ImportID),
				Detail: fmt.Sprintf(
					"A live %s carries estate %q and the tofu-address value %q, which is the address of a %s. A marker names the resource it is written on (see live/MARKERS.md), so nothing can be planned for it. Retag the resource with its own address, or remove the marker to disown it.",
					o.TypeName, req.Estate, o.Normalized, o.Addr.Resource.Resource.Type),
			}))
			o.Withheld = fmt.Sprintf(
				"its marker names a %s and the live resource is a %s, so no instance address describes it",
				o.Addr.Resource.Resource.Type, o.TypeName)
		case o.ImportID == "":
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemNoIdentity,
				TypeName: o.TypeName,
				Addr:     o.Addr,
				Marker:   o.Normalized,
				Detail: fmt.Sprintf(
					"A live %s carrying this estate's marker for %s came back from the list call with no usable identity, so there is nothing to read it with and no destroy can be planned for it. The identity this type is looked up by (%s) was not among the attributes the list call returned. A provider that serves no identity at all cannot be discovered by marker; one that serves a different set is issue #105.",
					o.TypeName, o.Addr, identityAttrNames(o.TypeName)),
			}))
			o.Withheld = "the provider served no identity for it, so it cannot be read or destroyed"
		case len(byAddr[o.Addr.String()]) > 1:
			diags = diags.Append(problemDiag(res, collisionOrphanProblem(req, res, byAddr[o.Addr.String()])))
			o.Withheld = fmt.Sprintf(
				"another live %s carries the same marker, and destroying one of two resources that claim one address would be a guess",
				o.TypeName)
		default:
			o.Removal = true
		}
	}

	for _, o := range res.Orphans {
		if !o.Removal {
			continue
		}
		// The same predicate internal/live/foreign's removal section reports
		// as BlockGone, through the one function, so that the plan's own
		// Undeclared flag and the sentence an operator reads beside it cannot
		// answer differently (issue #316).
		declared := identity.DeclaresBlock(req.Config, o.Addr)
		res.Resolutions = append(res.Resolutions, identity.Resolution{
			Addr:  o.Addr,
			Class: identity.ClassConcrete,
			// Both forms travel: the string for every line an operator
			// reads, and the provider's own identity object for the import
			// itself. See [identity.Resolution.Identity].
			ImportID:   o.ImportID,
			Identity:   o.Identity,
			Undeclared: !declared,
		})
	}

	return diags
}

// blockKey is the resource block one instance address belongs to, as
// [classifyOrphans]'s rename guard compares blocks: the type and name, with
// the instance key and the module path both taken off.
//
// Both sides of that comparison go through this, which is the point of it
// existing at all. Before issue #316 the declared side computed
// EscapeAddress(addr.Resource.Resource.String()) and the read side computed
// strings.Cut(marker, ":") - two readings of one grammar, agreeing by
// coincidence on a root-module address and on nothing else.
//
// The module path is deliberately not part of the key, and that is the one
// judgement in this function rather than a mechanical consequence of the fix.
// A module-qualified key is the narrower reading, and it is what the issue
// proposed; it is also strictly less safe than what the guard did before,
// because it stops withholding an orphan whose block was moved from the root
// into a module (or between two modules) while an instance of the moved block
// sits unclaimed. That refactor is ordinary, the marker for it has to be
// rewritten by hand either way, and destroying the live resource is the one
// outcome the guard exists to prevent. Keyed on type and name the guard is a
// strict superset of what it withheld before this fix: it can only ever
// withhold more, never less, so no configuration that planned no destroy
// before this change plans one after it.
//
// The cost of the wider key is an orphan of a genuinely deleted block
// lingering because an unrelated module happens to declare an unclaimed
// instance of the same type and name. That is the direction this function's
// caller says out loud it is willing to be wrong in: one command for an
// operator, rather than a resource nobody asked to touch being destroyed and
// recreated.
func blockKey(addr addrs.AbsResourceInstance) string {
	return addr.Resource.Resource.String()
}

// orphanBlockKey is [blockKey] for the other side of the comparison: the
// block an orphan's marker names.
//
// It reads the block off the decoded address, so the two sides are the same
// expression over the same type and cannot drift apart again.
//
// A marker that will not decode at all falls back to the text-level cut the
// guard used before, which is right for exactly the markers that cut was ever
// right for - root-module ones - and is kept because withholding runs BEFORE
// the malformed-marker report. A corrupt value like "aws_subnet.this:" sitting
// in a block that still has an unclaimed instance is withheld silently today;
// dropping the fallback would turn that silence into a hard error on an
// estate that plans cleanly now.
func orphanBlockKey(o *OwnedResource) string {
	if o.Addressable {
		return blockKey(o.Addr)
	}
	legacy, _, _ := strings.Cut(o.Normalized, ":")
	return legacy
}

// collisionOrphanProblem is the ownership collision of the undeclared: two
// or more live resources carrying one estate and one address, none of which
// the configuration declares. It is the same condition
// [collisionProblem] reports for a declared address, and it is refused for
// the same reason.
func collisionOrphanProblem(req Request, res *Result, idx []int) Problem {
	ids := make([]string, 0, len(idx))
	for _, i := range idx {
		id := res.Orphans[i].ImportID
		if id == "" {
			id = "(no identity)"
		}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	first := res.Orphans[idx[0]]
	return Problem{
		Kind:     ProblemCollision,
		TypeName: first.TypeName,
		Addr:     first.Addr,
		Marker:   first.Normalized,
		LiveIDs:  ids,
		Detail: fmt.Sprintf(
			"%d live %s resources carry estate %q and the address %q, which this configuration no longer declares: %s. This address names several live resources, so nothing is proposed for any of them until a human says which is which.",
			len(ids), first.TypeName, req.Estate, first.Normalized, strings.Join(ids, ", ")),
	}
}

// sweepGapDiag records a sweep gap on the result, and returns a diagnostic
// for the ones that are events rather than standing facts.
//
// No gap is ever an error. The plan in front of the operator is correct
// about everything the sweep did reach, and failing a run because the
// provider cannot list a type nothing in the configuration mentions would
// make the admission table impossible to grow.
//
// Only a failure raises a diagnostic. A type the provider does not list, or
// that carries no tags, is a standing property of the provider version and
// the type: it is the same on every run of every configuration, and a
// warning per type per run would bury the one that means something. Those
// are reported in the sweep-coverage section instead, where a reader sees
// the whole shape of the coverage at once. A list call that failed is the
// opposite - a fact about this run, and one that may not repeat - so it says
// so out loud.
func sweepGapDiag(res *Result, g SweepGap) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	res.SweepGaps = append(res.SweepGaps, g)
	if g.Reason == SweepGapNotListable || g.Reason == SweepGapNotTaggable {
		return diags
	}
	return diags.Append(tfdiags.Sourceless(
		tfdiagsSeverity(SeverityForRefusal(SummaryIncompleteSweep)),
		SummaryIncompleteSweep,
		g.Detail,
	))
}

// supportsTagFilter reports whether a type's list configuration accepts
// EC2-style filter blocks, and if not, why not - the reason travels into
// [TypeScan.FilterReason] so an operator reading a slow scan can see which
// type made it slow.
func supportsTagFilter(ts listclient.TypeSchema) (bool, string) {
	if ts.Config == nil {
		return false, "the type has no list configuration schema"
	}
	nested, ok := ts.Config.BlockTypes["filter"]
	if !ok {
		return false, fmt.Sprintf("the list configuration for %s has no filter argument", ts.TypeName)
	}
	if nested.Nesting != configschema.NestingList && nested.Nesting != configschema.NestingSet {
		return false, fmt.Sprintf("the filter argument of %s is not a repeatable block", ts.TypeName)
	}
	name, hasName := nested.Block.Attributes["name"]
	values, hasValues := nested.Block.Attributes["values"]
	if !hasName || !hasValues || name.Type != cty.String {
		return false, fmt.Sprintf("the filter block of %s is not the name/values shape a tag filter needs", ts.TypeName)
	}
	if !values.Type.IsListType() && !values.Type.IsSetType() {
		return false, fmt.Sprintf("the filter block of %s takes a %s for its values, not a collection", ts.TypeName, values.Type.FriendlyName())
	}
	return true, ""
}

// tagFilter builds the one filter block discovery ever sends: match a tag
// key to one value, server-side.
func tagFilter(key, value string) cty.Value {
	return cty.ListVal([]cty.Value{cty.ObjectVal(map[string]cty.Value{
		"name":   cty.StringVal("tag:" + key),
		"values": cty.ListVal([]cty.Value{cty.StringVal(value)}),
	})})
}

// moduleDisplay names a module instance for a diagnostic: "the root module",
// or "module.a.module.b" for a nested one.
func moduleDisplay(modInst addrs.ModuleInstance) string {
	if len(modInst) == 0 {
		return "the root module"
	}
	return modInst.String()
}

func hasAttr(b *configschema.Block, name string) bool {
	if b == nil {
		return false
	}
	_, ok := b.Attributes[name]
	return ok
}

// identityAttrNames renders the attributes importIdentity will look for, so
// a ProblemNoIdentity can say which ones it wanted rather than blaming the
// provider for serving none. It mirrors importIdentity's own defaulting
// exactly; keep the two in step.
func identityAttrNames(typeName string) string {
	attrs := []string{"id"}
	if ti, ok := identity.LookupType(typeName); ok && len(ti.IdentityAttrs) > 0 {
		attrs = ti.IdentityAttrs
	}
	return strings.Join(attrs, ", ")
}

// importIdentity reads the live import ID out of a list result, following
// the identity table's IdentityAttrs for the type - "id" for every EC2 type
// in the v0 subset, with aws_eip also accepting allocation_id - and falling
// back to "id" for a type the table does not cover.
func importIdentity(typeName string, r listclient.Result) (string, string, bool) {
	attrs := []string{"id"}
	if ti, ok := identity.LookupType(typeName); ok && len(ti.IdentityAttrs) > 0 {
		attrs = ti.IdentityAttrs
	}
	for _, attr := range attrs {
		if v, ok := r.IdentityAttr(attr); ok && v != "" {
			return v, attr, true
		}
	}
	return "", "", false
}

// liveIDs renders the import identities of the live resources a problem is
// about. A resource the provider sent no identity for still has to appear:
// "one of these two is unidentifiable" is information, and a problem naming
// nothing at all would be unactionable.
func liveIDs(ids ...string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			id = "(no identity)"
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// ---------------------------------------------------------------------------
// Binding
// ---------------------------------------------------------------------------

// bind turns the claims collected by the scan into bindings, unbound
// addresses and problems, and rewrites the resolution list.
func bind(req Request, decl *declared, res *Result) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics

	bound := make(map[string]Binding)

	for _, typeName := range decl.bindTypeNames() {
		if decl.unscanned[typeName] {
			// Nothing was listed for this type, and a problem already says
			// why. Calling its instances unbound here would tell the plan
			// to create resources that may well exist.
			continue
		}
		// Count blocks first: their instances are a set, and the set matcher
		// owns every one of them at once.
		for _, cb := range sortedCountBlocks(decl.counts[typeName]) {
			diags = diags.Append(bindCountBlock(req, cb, res, bound))
		}

		for _, escaped := range decl.order[typeName] {
			entry := decl.types[typeName][escaped]
			if entry.inCount {
				continue
			}
			switch len(entry.claimants) {
			case 0:
				// Nothing claims this address. Absence is the answer, and
				// the plan proposing a create is the correct outcome -
				// unless the run listed resources of this type it could not
				// read a marker off, in which case absence is one of two
				// answers and #266 says which one out loud.
				res.Unbound = append(res.Unbound, entry.res.Addr)
				if p, ok := unreadableMarkerProblem(req, decl, typeName, escaped, entry.res.Addr); ok {
					diags = diags.Append(problemDiag(res, p))
				}
			case 1:
				if diag, hasProblem := bindClaimant(res, bound, typeName, escaped, entry.res.Addr, entry.claimants[0]); hasProblem {
					diags = diags.Append(diag)
				}
			default:
				// GitHub issue #361's crash-window recovery: a
				// create-before-destroy replace interrupted after the new
				// object commits but before the old one is destroyed
				// leaves BOTH objects carrying this address's marker,
				// which is exactly this shape. If the estate's record
				// names a deposed object for this address, and exactly
				// one of the claimants here is that object (already
				// live-read and marker-verified by this pass, ahead of
				// ever consulting the record - #361's design comment,
				// section 4), it is not a genuine collision: pull it out
				// and bind the remaining single claimant through the
				// ordinary case-1 path. Any other count - zero matches,
				// or the record naming an object none of these claimants
				// is - falls through to the ordinary collision refusal
				// below unchanged.
				if rec, dk, idx, ok := matchDeposedClaimant(req, entry.res.Addr, entry.claimants); ok {
					remainingIdx, remainingCount := -1, 0
					for i := range entry.claimants {
						if i == idx {
							continue
						}
						remainingIdx, remainingCount = i, remainingCount+1
					}
					if remainingCount == 1 {
						res.DeposedBindings = append(res.DeposedBindings, projection.NewDeposedBinding(entry.res.Addr, dk, rec))
						if diag, hasProblem := bindClaimant(res, bound, typeName, escaped, entry.res.Addr, entry.claimants[remainingIdx]); hasProblem {
							diags = diags.Append(diag)
						}
						break
					}
				}
				diags = diags.Append(problemDiag(res, collisionProblem(req, typeName, entry)))
			}
		}

		// Markers naming a whole for_each block rather than one of its
		// instances: pre-instance-key writers. Never bound by guess, and
		// never resolvable by slots either - a for_each instance's key is
		// its identity, not a position in a fungible set.
		//
		// This is an ENUMERATION of a map a moved block aliases: since
		// GitHub issue #198, d.blocks files one *declaredBlock under its
		// own address AND under every address a moved block says it used to
		// have, so ranging the map visits the same block once per name it
		// answers to. Reported per name, one live resource carrying one
		// block-level marker produced two identical problems - the same
		// shape as the two the moved work's own author found in d.types and
		// d.counts, in the third and last read of that kind. Keyed on the
		// block pointer rather than on blk.addr because the pointer is what
		// the aliases actually share.
		reported := make(map[*declaredBlock]bool)
		for _, blockAddr := range sortedBlockAddrs(decl.blocks[typeName]) {
			blk := decl.blocks[typeName][blockAddr]
			if len(blk.claimants) == 0 || reported[blk] {
				continue
			}
			reported[blk] = true
			diags = diags.Append(problemDiag(res, blockLevelMarkerProblem(typeName, blk)))
		}
	}

	// The bound count is only knowable once claims are resolved, so the
	// scan rows are completed here rather than during the scan.
	perType := make(map[string]int, len(res.Bindings))
	for _, b := range res.Bindings {
		perType[b.TypeName]++
	}
	for i := range res.Scans {
		res.Scans[i].Bound = perType[res.Scans[i].TypeName]
	}

	if len(bound) > 0 {
		for i, r := range res.Resolutions {
			b, ok := bound[r.Addr.String()]
			if !ok {
				continue
			}
			res.Resolutions[i] = identity.Resolution{
				Addr:     r.Addr,
				Class:    identity.ClassConcrete,
				ImportID: b.ImportID,
				Identity: b.Identity,
			}
		}
	}

	// Surplus members are not declared anywhere, so they are appended rather
	// than rewritten. They reach the projection as concrete resolutions at
	// the instance addresses just above the declared count, which is how a
	// shrunken count's leftovers appear in a stock run's prior state - and
	// from there the plan engine's own orphan handling proposes destroying
	// them, with nothing in stateless mode teaching it anything about slots.
	for _, s := range res.Surplus {
		res.Resolutions = append(res.Resolutions, identity.Resolution{
			Addr:     s.Addr,
			Class:    identity.ClassConcrete,
			ImportID: s.ImportID,
			Identity: s.Identity,
		})
	}
	return diags
}

// blockLevelMarkerProblem is the [ProblemNeedsSlotMarkers] a for_each block
// gets when live resources carry the block's own address rather than one of
// its instance keys.
//
// It names the markers the claimants actually carried, which is GitHub issue
// #248. Since #198 a *declaredBlock also answers to every address a moved
// block says it used to have, so a claimant can reach this block under a
// name that is not blk.addr - and blk.addr is what the message used to
// print. An operator following a moved block holds the origin address and
// was handed the destination: greping their cloud tags for it finds nothing,
// and the remedy ("rewrite each resource's tofu-address") gave them no way
// to locate the resource it was about. Problem.Marker carried the same wrong
// value, against its own field documentation - "the tofu-address value
// involved, escaped as compared" is claimant.escaped, never the block's
// canonical name.
//
// The live IDs go into the sentence for the same reason (#248's third
// option, taken as well as the first rather than instead of it): they are
// what an operator can act on directly, and they were already on the Problem
// without ever appearing in the text.
//
// A claimant that carried the block's own address gets today's wording
// unchanged - that is the overwhelmingly common case and it was never wrong.
func blockLevelMarkerProblem(typeName string, blk *declaredBlock) Problem {
	carried := carriedMarkers(blk.claimants)
	ids := claimantIDs(blk.claimants)

	// Marker holds the value compared when there is one of them. Several
	// claimants carrying several spellings of one block have no single such
	// value, so the block's own address stands in and the sentence names
	// every spelling.
	marker := blk.addr
	if len(carried) == 1 {
		marker = carried[0]
	}

	var whichBlock string
	if len(carried) != 1 || carried[0] != blk.addr {
		whichBlock = fmt.Sprintf(", which this configuration now declares as %q", blk.addr)
	}

	return Problem{
		Kind:     ProblemNeedsSlotMarkers,
		TypeName: typeName,
		Marker:   marker,
		LiveIDs:  ids,
		Detail: fmt.Sprintf(
			"%d live %s resource(s) (%s) carry the marker %s%s, which is the address of a for_each block with %d expanded instances rather than the address of any one of them. Nothing distinguishes which live resource is which instance. Rewrite each resource's tofu-address to the keyed address it belongs to; until then these instances stay unbound.",
			len(blk.claimants), typeName, strings.Join(ids, ", "), quotedList(carried), whichBlock, blk.instances),
	}
}

// carriedMarkers is the distinct escaped tofu-address values a set of
// claimants actually carried, sorted. Usually one; more than one when a
// moved block's origin and destination spellings are both live at once,
// which is exactly what an interrupted marker rewrite leaves behind.
func carriedMarkers(cs []claimant) []string {
	seen := make(map[string]bool, len(cs))
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		if seen[c.escaped] {
			continue
		}
		seen[c.escaped] = true
		out = append(out, c.escaped)
	}
	sort.Strings(out)
	return out
}

// quotedList renders one or more values as an operator would read them:
// `"a"`, or `"a" and "b"`, or `"a", "b" and "c"`.
func quotedList(vals []string) string {
	quoted := make([]string, len(vals))
	for i, v := range vals {
		quoted[i] = fmt.Sprintf("%q", v)
	}
	switch len(quoted) {
	case 0:
		return ""
	case 1:
		return quoted[0]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " and " + quoted[len(quoted)-1]
	}
}

// collisionProblem distinguishes the two ways several live resources come to
// claim one address: a genuine ownership collision, and a set of count
// instances that phase 3's slot markers exist to tell apart.
// bindClaimant turns one claimant into a [Binding] and records it in res and
// bound, or raises [ProblemNoIdentity] when the claimant carries no usable
// identity - the same construction bind()'s own case-1 arm always did,
// factored out so the deposed-disambiguation arm can reuse it exactly
// rather than the two diverging over time. hasProblem is false whenever a
// Binding was recorded; diag is only meaningful when it is true.
func bindClaimant(res *Result, bound map[string]Binding, typeName, escaped string, addr addrs.AbsResourceInstance, c claimant) (diag tfdiags.Diagnostic, hasProblem bool) {
	if c.noIdentity {
		return problemDiag(res, Problem{
			Kind:     ProblemNoIdentity,
			TypeName: typeName,
			Addr:     addr,
			Marker:   escaped,
			Detail: fmt.Sprintf(
				"The live %s carrying the marker for %s came back from the list call with no usable identity, so there is no import ID to build a projection from. The identity this type is looked up by (%s) was not among the attributes the list call returned. A provider that serves no identity at all cannot be discovered by marker; one that serves a different set is issue #105.",
				typeName, addr, identityAttrNames(typeName)),
		}), true
	}
	b := Binding{
		Addr:         addr,
		TypeName:     typeName,
		ImportID:     c.importID,
		IdentityAttr: c.identityAttr,
		Marker:       c.marker,
		Normalized:   c.normalized,
		Slot:         c.slot,
		DisplayName:  c.displayName,
		Identity:     c.identity,
	}
	res.Bindings = append(res.Bindings, b)
	bound[addr.String()] = b
	return nil, false
}

// matchDeposedClaimant is GitHub issue #361's crash-window disambiguation:
// it checks addr's claimants against req.DeposedRecords, returning the one
// (deposed key, record) pair a claimant matched and that claimant's own
// index into claimants - but only when EXACTLY one (claimant, candidate)
// pair matches across the whole set. Any other count - no recorded
// candidate for addr, no claimant matching any of them, or more than one
// match - returns ok false: [collisionProblem] is the correct, safe
// default for every one of those, and this function guesses through none
// of them.
func matchDeposedClaimant(req Request, addr addrs.AbsResourceInstance, claimants []claimant) (rec projection.DeposedRecord, deposedKey string, claimantIdx int, ok bool) {
	perAddr := req.DeposedRecords[addr.String()]
	if len(perAddr) == 0 {
		return projection.DeposedRecord{}, "", -1, false
	}
	matches := 0
	claimantIdx = -1
	for dk, candidate := range perAddr {
		for i, c := range claimants {
			if deposedClaimantMatches(candidate, c) {
				matches++
				rec, deposedKey, claimantIdx = candidate, dk, i
			}
		}
	}
	if matches != 1 {
		return projection.DeposedRecord{}, "", -1, false
	}
	return rec, deposedKey, claimantIdx, true
}

// deposedClaimantMatches reports whether a live claimant is the object rec
// names: by import ID for a type identified by one server-minted string, or
// by every named identity-schema component for a composite type. Generic by
// construction - no resource type name appears in this function, only the
// property (identified by one string, or by several named components) every
// admitted type already has one of.
func deposedClaimantMatches(rec projection.DeposedRecord, c claimant) bool {
	if rec.ImportID != "" {
		return rec.ImportID == c.importID
	}
	if len(rec.Components) == 0 {
		return false
	}
	if c.identity == cty.NilVal || c.identity.IsNull() || !c.identity.IsKnown() || c.identity.IsMarked() || !c.identity.Type().IsObjectType() {
		return false
	}
	ty := c.identity.Type()
	for name, want := range rec.Components {
		if !ty.HasAttribute(name) {
			return false
		}
		v := c.identity.GetAttr(name)
		// v.IsMarked() before AsString(): cty panics rather than errors on
		// a marked receiver, and a sensitive input variable is the
		// ordinary way to produce one. A marked component simply does not
		// match - refused, never unmarked, since the alternative is
		// letting a value nothing here proved safe flow into an identity
		// comparison.
		if v.IsMarked() || v.IsNull() || !v.IsKnown() || v.Type() != cty.String || v.AsString() != want {
			return false
		}
	}
	return true
}

func collisionProblem(req Request, typeName string, entry *declaredEntry) Problem {
	ids := claimantIDs(entry.claimants)

	if _, isCount := entry.res.Addr.Resource.Key.(addrs.IntKey); isCount {
		return Problem{
			Kind:     ProblemNeedsSlotMarkers,
			TypeName: typeName,
			Addr:     entry.res.Addr,
			Marker:   entry.escaped,
			LiveIDs:  ids,
			Detail: fmt.Sprintf(
				"%d live %s resources claim the count instance %s (%s). Count instances are a fungible set, so their shared address does not say which is which; tofu-slot markers do (see live/MARKERS.md, \"tofu-slot\"). Discovery will not pick one.",
				len(ids), typeName, entry.res.Addr, strings.Join(ids, ", ")),
		}
	}
	return Problem{
		Kind:     ProblemCollision,
		TypeName: typeName,
		Addr:     entry.res.Addr,
		Marker:   entry.escaped,
		LiveIDs:  ids,
		Detail: fmt.Sprintf(
			"%d live %s resources carry estate %q and address %q at once: %s. A human has to resolve the collision before this estate can be planned; see live/MARKERS.md, \"Ownership semantics\".",
			len(ids), typeName, req.Estate, entry.escaped, strings.Join(ids, ", ")),
	}
}

func claimantIDs(cs []claimant) []string {
	out := make([]string, 0, len(cs))
	for _, c := range cs {
		id := c.importID
		if id == "" {
			id = "(no identity)"
		}
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func sortedBlockAddrs(m map[string]*declaredBlock) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// tfdiagsSeverity is the tfdiags spelling of a [Severity]. Both diagnostic
// builders in this package ([problemDiag] and [sweepGapDiag]) go through it,
// so [SeverityForRefusal] is the one place that decides what an operator
// sees and what live/LIMITATIONS.md says at once.
func tfdiagsSeverity(s Severity) tfdiags.Severity {
	if s == SeverityWarning {
		return tfdiags.Warning
	}
	return tfdiags.Error
}

// problemDiag records a problem on the result and returns the diagnostic
// that goes with it, so the two can never drift apart.
func problemDiag(res *Result, p Problem) tfdiags.Diagnostic {
	res.Problems = append(res.Problems, p)

	severity := tfdiagsSeverity(p.Kind.Severity())

	// Every kind in problemSummaries has a summary of its own, and
	// TestProblemSummariesCoverKinds keeps it that way.
	//
	// The fallback used to interpolate the kind into the summary, which
	// reads better and cannot be registered: a summary assembled at runtime
	// is not a string any table can hold, so the one diagnostic here that
	// means "nobody has classified this yet" was also the one refusal
	// live/LIMITATIONS.md could not describe. The kind moves into the
	// detail, where it is just as findable by an operator reporting it and
	// where it does not put a hole in the registry. See #110.
	summary := problemSummaries[p.Kind]
	unclassified := summary == ""
	if unclassified {
		summary = SummaryUnclassifiedProblem
	}

	// The live IDs are appended as their own sentence, so the detail has to
	// end as one. Every Problem.Detail in this package does; a later one that
	// forgets gets the period here rather than a run-on line in the output.
	detail := strings.TrimRight(p.Detail, " ")
	if unclassified {
		detail = fmt.Sprintf("Discovery problem kind %q has no summary of its own, which is a gap in this package rather than something the configuration did. %s", p.Kind, detail)
	}
	if len(p.LiveIDs) > 0 && !strings.Contains(detail, p.LiveIDs[0]) {
		if detail != "" && !strings.HasSuffix(detail, ".") && !strings.HasSuffix(detail, ":") {
			detail += "."
		}
		detail += " Live resources: " + strings.Join(p.LiveIDs, ", ") + "."
	}
	return tfdiags.Sourceless(severity, summary, detail)
}

var problemSummaries = map[ProblemKind]string{
	ProblemCollision:               "Two live resources claiming one address",
	ProblemMalformedMarker:         "Malformed ownership marker",
	ProblemDisplacedMarker:         "Live resource displaced from the address it is marked for",
	ProblemNeedsSlotMarkers:        "Indistinguishable instances without per-instance markers",
	ProblemMixedSlots:              "Partial slot markers on a count set",
	ProblemMalformedSlot:           "Malformed slot marker",
	ProblemDuplicateSlot:           "Two live resources claiming one slot",
	ProblemSlotExhausted:           "No slot left to mint",
	ProblemNoIdentity:              "Listed resource with no identity",
	ProblemNoTags:                  "Listed resource with no tags",
	ProblemTypeNotListable:         "Unlistable marker-discovered type",
	ProblemLocatedRecordUnreadable: "Located identity record unreadable",
	ProblemUnresolvedAccount:       "No AWS account ID from the provider",
	ProblemListFailed:              "Failed to list a resource type",
	ProblemRecordStoreListFailed:   "Cannot list the record store",
	ProblemUncomposableIdentifier:  "Cloud Control identifier could not be composed",
	ProblemAmbiguousUniqueName:     "Unique name matched more than one resource",
	ProblemUnreadableUniqueName:    "Listed resource with no readable name",
	ProblemUnresolvedTaggedARN:     "Tagged resource's ARN could not be joined to a resource type",
	ProblemUnsweepableOwnedType:    "Owned resource of a type the sweep cannot cover",
	ProblemAmbiguousTagJoin:        "Listed resource matched more than one tagged resource",
	ProblemUnreadableMarker:        "Unbound instance with unreadable live markers of its type",
	ProblemAmbiguousContentMatch:   "Content match found more than one live candidate",
}
