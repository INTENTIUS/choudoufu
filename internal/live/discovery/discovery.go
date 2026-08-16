// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package discovery

import (
	"context"
	"fmt"
	"log"
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

	// HintStore is the estate's record store, the one carrier the guided
	// hint has (issue #109) - ordinarily the same store [Request]'s caller
	// opened from the live block's record_store block. Read through
	// [projection.ReadHintStore], at the key [projection.HintKey](Estate)
	// derives. Nil disables the hint read, which under Guided means every
	// pass falls back to full enumeration.
	HintStore staterecord.Store

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

	decl, declDiags := declaredInstances(ctx, req)
	diags = diags.Append(declDiags)
	if declDiags.HasErrors() {
		return res, diags
	}
	if len(decl.types) == 0 && !req.Sweep {
		// Nothing waits on discovery and no sweep was asked for, which is a
		// legitimate configuration: every instance was named by static
		// analysis, and without a sweep there is nothing else to look at.
		res.sortEverything()
		return res, diags
	}

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
			// per-type loop below. See [sweepViaTagging] and
			// [Request.TaggingSweep]. It is one round trip rather than one
			// per type, so it gets no progress events of its own - there is
			// nothing to report between, only before and after.
			diags = diags.Append(sweepViaTagging(ctx, req, decl, res))
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
		out = append(out, t)
	}
	sort.Strings(out)
	return out
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

	order map[string][]string // type -> escaped instance addresses, in address order

	// unscanned holds the types whose scan never happened - the provider
	// cannot list them, or listing errored. Their declared instances are
	// not reported as unbound, because "nothing claims this address" and
	// "nobody looked" are different answers and only the first one means
	// the plan should propose creating something.
	unscanned map[string]bool

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
	all map[string]map[string]bool
}

// declares reports whether the configuration declares the given escaped
// instance address for the given resource type.
func (d *declared) declares(typeName, escaped string) bool {
	return d.all[typeName][escaped]
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
		order:        make(map[string][]string),
		unscanned:    make(map[string]bool),
		all:          make(map[string]map[string]bool, len(req.Resolutions)),
	}

	// The moved blocks this configuration's markers can follow (GitHub issue
	// #198), computed once. Every alias below is derived from this list, and
	// internal/live/lint refuses exactly the statements it leaves out, so a
	// block that passes lint is a block whose old address is indexed here.
	movedStmts := moved.Honoured(req.Config)

	for _, r := range req.Resolutions {
		typeName := r.Type()
		if d.all[typeName] == nil {
			d.all[typeName] = make(map[string]bool)
		}
		raw := r.Addr.String()
		escaped := EscapeAddress(raw)
		d.all[typeName][escaped] = true
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
			d.all[typeName][EscapeAddress(origin.String())] = true
		}
		if legacy := LegacyEscapeAddress(raw); legacy != escaped {
			// A for_each key containing "@" - the one character both the
			// pre- and post-issue-#178 grammars admit but escape
			// differently - is also declared under the address a prior run
			// would have written, so a client-named instance (the only
			// consumer of d.all) that predates the widened grammar is still
			// recognized as declared. See markers.AddressMatches's doc
			// comment.
			d.all[typeName][legacy] = true
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
		if d.types[typeName] == nil {
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
		var forEach hcl.Expression
		if call, ok := cfg.Module.ModuleCalls[name]; ok && call != nil {
			forEach = call.ForEach
		}
		keys, diag := identity.ChildModuleKeys(ctx, cfg.Module, fmt.Sprintf("module %q", name), forEach)
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
		// No native list resource. Before refusing, see whether Cloud
		// Control can enumerate this type instead (issue #47): the mapped
		// CFN type has to exist and be listable with no required input, and
		// a caller has to have configured a Cloud Control client at all -
		// nil Request.CloudControl is "the fallback does not apply here",
		// not an error, so every existing caller that never heard of Cloud
		// Control keeps today's refusal unchanged.
		if cfnType, ccOK := cloudControlSource(req, typeName); ccOK {
			return scanTypeCloudControl(ctx, req, decl, typeName, cfnType, res, sweep)
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

		if markerType := markerTypeOf(escaped); markerType != typeName {
			// The estate owns this resource and its marker names an address
			// of another type. Nothing can be done with it: binding it to the
			// address it names would attach a plan for one resource type to a
			// resource of another, and ignoring it would leave a resource this
			// estate owns invisible to every section of the output. So it is
			// the marker spec's third answer - malformed - and a human says
			// which address it belongs to. (Audit finding C4: this used to
			// match the declared-address set, which carried no type, and the
			// resource was silently dropped.)
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

		c := claimant{
			importID:     importID,
			identityAttr: idAttr,
			identity:     r.Identity,
			displayName:  r.DisplayName,
			marker:       raw,
			escaped:      escaped,
			normalized:   escaped != raw,
			slot:         tags[TagSlot],
			tags:         tags,
			noIdentity:   !hasID,
		}

		if entry, ok := decl.entryFor(typeName, escaped); ok {
			entry.claimants = append(entry.claimants, c)
			continue
		}
		if decl.declares(typeName, escaped) {
			// A declared instance whose identity came out of the
			// configuration rather than out of a marker: nothing was waiting
			// to be found here, the projection reads it by the identity the
			// configuration gives, and the marker only confirms the estate
			// still owns what it thinks it owns. Seen mostly by the sweep,
			// which is the only thing that lists such types at all.
			continue
		}
		// A marker naming a count block, by its bare address or by an index
		// the configuration no longer expands to. It is not an orphan and
		// not a stranger: it is a member of that block's set whose position
		// in the set its address does not settle, which is exactly the
		// question slots answer. Parking it on the block hands it to the set
		// matcher, which either binds it by slot or - for an estate with no
		// slots - puts it back where it was.
		if cb := decl.countBlockFor(typeName, escaped); cb != nil {
			cb.extra = append(cb.extra, c)
			continue
		}
		if blk, ok := decl.blocks[typeName][escaped]; ok && blk.keyed {
			// The marker names the resource block, not one of its
			// instances: markers written before instance keys were part of
			// the address. For a for_each block nothing distinguishes which
			// live resource is which declared instance, and the address is
			// the only identity a for_each instance has.
			blk.claimants = append(blk.claimants, c)
			continue
		}
		res.Orphans = append(res.Orphans, OwnedResource{
			TypeName:     typeName,
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
		pending[EscapeAddress(addr.Resource.Resource.String())] = true
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
		block, _, _ := strings.Cut(o.Normalized, ":")

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
		declared := false
		if modCfg, ok := identity.ConfigForModule(req.Config, o.Addr.Module); ok && modCfg.Module != nil {
			_, declared = modCfg.Module.ManagedResources[o.Addr.Resource.Resource.String()]
		}
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
		tfdiags.Warning,
		"Incomplete sweep for undeclared resources",
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

	for _, typeName := range decl.typeNames() {
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
				// the plan proposing a create is the correct outcome.
				res.Unbound = append(res.Unbound, entry.res.Addr)
			case 1:
				c := entry.claimants[0]
				if c.noIdentity {
					diags = diags.Append(problemDiag(res, Problem{
						Kind:     ProblemNoIdentity,
						TypeName: typeName,
						Addr:     entry.res.Addr,
						Marker:   escaped,
						Detail: fmt.Sprintf(
							"The live %s carrying the marker for %s came back from the list call with no usable identity, so there is no import ID to build a projection from. The identity this type is looked up by (%s) was not among the attributes the list call returned. A provider that serves no identity at all cannot be discovered by marker; one that serves a different set is issue #105.",
							typeName, entry.res.Addr, identityAttrNames(typeName)),
					}))
					continue
				}
				b := Binding{
					Addr:         entry.res.Addr,
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
				bound[entry.res.Addr.String()] = b
			default:
				diags = diags.Append(problemDiag(res, collisionProblem(req, typeName, entry)))
			}
		}

		// Markers naming a whole for_each block rather than one of its
		// instances: pre-instance-key writers. Never bound by guess, and
		// never resolvable by slots either - a for_each instance's key is
		// its identity, not a position in a fungible set.
		for _, blockAddr := range sortedBlockAddrs(decl.blocks[typeName]) {
			blk := decl.blocks[typeName][blockAddr]
			if len(blk.claimants) == 0 {
				continue
			}
			diags = diags.Append(problemDiag(res, Problem{
				Kind:     ProblemNeedsSlotMarkers,
				TypeName: typeName,
				Marker:   blk.addr,
				LiveIDs:  claimantIDs(blk.claimants),
				Detail: fmt.Sprintf(
					"%d live %s resource(s) carry the marker %q, which is the address of a for_each block with %d expanded instances rather than the address of any one of them. Nothing distinguishes which live resource is which instance. Rewrite each resource's tofu-address to the keyed address it belongs to; until then these instances stay unbound.",
					len(blk.claimants), typeName, blk.addr, blk.instances),
			}))
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

// collisionProblem distinguishes the two ways several live resources come to
// claim one address: a genuine ownership collision, and a set of count
// instances that phase 3's slot markers exist to tell apart.
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

// problemDiag records a problem on the result and returns the diagnostic
// that goes with it, so the two can never drift apart.
func problemDiag(res *Result, p Problem) tfdiags.Diagnostic {
	res.Problems = append(res.Problems, p)

	severity := tfdiags.Error
	if p.Kind.Severity() == SeverityWarning {
		severity = tfdiags.Warning
	}

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
	ProblemCollision:              "Two live resources claiming one address",
	ProblemMalformedMarker:        "Malformed ownership marker",
	ProblemNeedsSlotMarkers:       "Indistinguishable instances without per-instance markers",
	ProblemMixedSlots:             "Partial slot markers on a count set",
	ProblemMalformedSlot:          "Malformed slot marker",
	ProblemDuplicateSlot:          "Two live resources claiming one slot",
	ProblemSlotExhausted:          "No slot left to mint",
	ProblemNoIdentity:             "Listed resource with no identity",
	ProblemNoTags:                 "Listed resource with no tags",
	ProblemTypeNotListable:        "Unlistable marker-discovered type",
	ProblemUnresolvedAccount:      "No AWS account ID from the provider",
	ProblemListFailed:             "Failed to list a resource type",
	ProblemUncomposableIdentifier: "Cloud Control identifier could not be composed",
	ProblemUnresolvedTaggedARN:    "Tagged resource's ARN could not be joined to a resource type",
	ProblemUnsweepableOwnedType:   "Owned resource of a type the sweep cannot cover",
}
