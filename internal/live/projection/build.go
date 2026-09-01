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
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/configs/configschema"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/live/markers"
	"github.com/intentius/choudoufu/internal/live/moved"
	"github.com/intentius/choudoufu/internal/live/noimporter"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/plans/objchange"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/states"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Build materializes a projection: an in-memory prior state holding the
// live object for every resource instance whose identity the identity
// package could produce and whose live counterpart the provider could
// find.
//
// cfg is the configuration the resolutions came from; it supplies each
// instance's provider configuration address and the references that become
// the recorded dependencies. resolutions is the identity package's output.
// provs supplies configured provider instances (see [Providers]).
//
// Error diagnostics mean the projection is untrustworthy and the caller
// must abort the run: a provider errored, or misbehaved, or the
// configuration and the resolutions disagree. An instance that is merely
// absent from the live system is not an error; it is recorded in
// [Result.Omitted] and the subsequent plan will propose creating it.
//
// Build writes no files and takes no locks. The returned state is the
// caller's to use for one operation and then drop.
func Build(ctx context.Context, cfg *configs.Config, resolutions *identity.Result, provs Providers) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	empty := &Result{State: states.NewState()}

	switch {
	case cfg == nil || cfg.Module == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to project",
			"Building a projection requires the configuration the identity resolutions were computed from, and none was given.",
		))
		return empty, diags
	case resolutions == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No identity resolutions to project",
			"Building a projection requires the output of identity resolution, and none was given.",
		))
		return empty, diags
	}

	return BuildFrom(ctx, cfg, resolutions.All(), provs)
}

// Options are the settings a projection needs that the resolutions and the
// configuration do not supply between them.
type Options struct {
	// UndeclaredProvider is the provider configuration to read instances
	// marked [identity.Resolution.Undeclared] through: resources this estate
	// owns whose resource block was deleted, which therefore have no
	// configuration to read a provider from.
	//
	// It is the provider configuration the marker sweep listed them with,
	// and that is the only defensible answer. A deleted block's provider
	// alias is not recoverable from anything, and a resource found by
	// listing through one account and region is in that account and region;
	// reading it through any other configuration would be reading somewhere
	// else. The zero value falls back to the provider the resource type
	// implies in the root module, which is right whenever the configuration
	// has one unaliased provider - the shape stateless mode v0 discovers
	// through anyway.
	//
	// [Options.UndeclaredProviders] takes precedence over this field per
	// instance, when it has an entry; this field is then only the fallback
	// for an undeclared instance the map does not name. A single-provider
	// caller can keep setting only this field, exactly as every caller did
	// before issue #69.
	UndeclaredProvider addrs.AbsProviderConfig

	// UndeclaredProviders overrides UndeclaredProvider per resolved instance
	// address (keyed by [addrs.AbsResourceInstance.String]), for an estate
	// whose managed resources span more than one provider configuration
	// (issue #69, aliased providers - typically multi-region). A sweep run
	// once per distinct provider configuration
	// ([internal/command/live_plan.go]'s statelessDiscover,
	// [discovery.Merge]) attributes each undeclared resource it finds to the
	// provider configuration that found it, because that account and region
	// is where the resource actually lives and any other provider
	// configuration would read the wrong place.
	//
	// Nil for a single-provider estate, which is what keeps
	// UndeclaredProvider alone sufficient and this package's behavior
	// byte-identical to before issue #69 whenever there is only one provider
	// configuration to begin with.
	UndeclaredProviders map[string]addrs.AbsProviderConfig

	// Ownership is the rule deciding which live objects may enter the prior
	// state. Nil means no check, which is what a caller that has no estate
	// concept at all - the marker rewrite in internal/live/mv, reading
	// one resource it was handed the identity of - passes. Every path that
	// builds a prior state for a plan sets it. See [Ownership].
	Ownership *Ownership

	// RecordStore is where GitHub issue #364's one per-instance envelope
	// lives: GitHub issue #73's record-backed resource instances
	// (identity.ClassRecordBacked, kind=object), GitHub issue #270's
	// record-located import identities (identity.ClassRecordLocated,
	// kind=identity), GitHub issue #275's argument-level residue and GitHub
	// issue #353's provisioner-taint bit, the last two independent of class
	// and of each other.
	//
	// One store rather than four: since the envelope collapse, only the
	// "kind" a key resolves to decides whether
	// builder.discoverOrphanedRecords may propose destroying it, not which
	// literal namespace root it lived under - see record.go's package
	// comment. Nil means no store: since internal/live/lint refuses every
	// RECORD_ADMITTED or record-located type before resolution runs unless
	// a live block configures one, a resolution of either class ordinarily
	// only arrives here when this is also set - see builder.materializeRecord
	// and builder.materializeLocated for the defensive path when it is not.
	// A residue or provisioner-taint instance with no store simply keeps
	// the behavior it had before either mechanism existed, which is
	// visible rather than silent.
	RecordStore *RecordStore

	// DataResults is GitHub issue #179's data-read phase output
	// (internal/command's statelessDataReads, the same map
	// identity.Context.DataResults takes and [identity.DataLookupFor]
	// already knows how to index), threaded through so [configuredTagsSeed]
	// and [configuredAttrsSeed] can resolve an argument that reads a data
	// source rather than only var/local/path/terraform.
	//
	// aws_launch_configuration.user_data_base64 is why this exists:
	// `base64encode(data.template_file.userdata.*.rendered[count.index])`
	// needs the SAME data source read result identity resolution already
	// obtained, or [configuredAttrsSeed]'s bare module-level evaluator
	// refuses it exactly as [configuredTagsSeed]'s own doc comment already
	// says a non-static "tags" argument does ("A resource whose 'tags'
	// argument is not statically evaluable ... is left alone") - correctly
	// safe, but needlessly narrow once the estate already paid for the
	// read. Nil is every caller before this field existed, and leaves both
	// seed functions exactly as they behaved before: no data source
	// resolves, only var/local/path/terraform/managed-resource-argument
	// references do.
	DataResults map[string]cty.Value

	// DeposedBindings is GitHub issue #361's crash-window recovery input:
	// [discovery.Result.DeposedBindings], the deposed objects discovery's
	// collision-breaking branch matched against this estate's record.
	// [buildFrom] reads each one live (the same import+read machinery an
	// ordinary current-object binding uses) and folds the result into
	// Instances[key].Deposed[dk] on the constructed state - see
	// [DeposedBinding]'s own doc comment for why that is the whole of what
	// this does: everything downstream (refresh, the proposed destroy) is
	// stock's own unmodified deposed-object graph machinery.
	//
	// Nil (every caller before this field existed) folds nothing in,
	// leaving BuildWith's output byte-identical to before.
	DeposedBindings []DeposedBinding

	// StateCache is a previously written state snapshot to use as a CANDIDATE
	// set for this build, or nil for none. Issue #685.
	//
	// It is never trusted on its own. A cached object is used in place of a
	// provider read only for an instance the estate sweep independently
	// verified in this run, by finding a live object carrying that instance's
	// own tofu-address marker - see [builder.cacheHit]. So a stale or absent
	// cache costs reads and cannot cost correctness, which is the property
	// that makes keeping one safe here and unsafe for stock OpenTofu.
	StateCache *states.State

	// ReadParallelism is how many of the read pass's per-instance provider
	// round trips - one ImportResourceState plus one ReadResource each -
	// this projection has in flight at once. Zero, the zero value, means
	// [DefaultReadParallelism]; one reproduces the sequential loop exactly.
	// See readconcurrency.go (GitHub issue #585) for what is and is not
	// overlapped, and why the default is what it is.
	ReadParallelism int
}

// BuildWith is [BuildFrom] with options. See [Options].
func BuildWith(ctx context.Context, cfg *configs.Config, resolutions []identity.Resolution, provs Providers, opts Options) (*Result, tfdiags.Diagnostics) {
	return buildFrom(ctx, cfg, resolutions, provs, opts)
}

// BuildFrom is [Build] over a plain list of resolutions rather than the
// identity package's Result.
//
// It exists for two reasons. P2's discovery pass produces resolutions that
// did not come out of static analysis - a marker lookup turns a
// needs-discovery instance into a concrete one - and it needs a way to
// hand the projection builder a merged list without reaching into another
// package's private structure. And a list of [identity.Resolution] values
// is something a test can write down by hand, which a Result is not, so
// the parent-derived paths through this package are testable before P2
// makes them reachable in practice.
//
// The list may hold resolutions in any order; ordering is this function's
// job.
func BuildFrom(ctx context.Context, cfg *configs.Config, resolutions []identity.Resolution, provs Providers) (*Result, tfdiags.Diagnostics) {
	return buildFrom(ctx, cfg, resolutions, provs, Options{})
}

func buildFrom(ctx context.Context, cfg *configs.Config, resolutions []identity.Resolution, provs Providers, opts Options) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	empty := &Result{State: states.NewState()}

	switch {
	case cfg == nil || cfg.Module == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No configuration to project",
			"Building a projection requires the configuration the identity resolutions were computed from, and none was given.",
		))
		return empty, diags
	case provs == nil:
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No provider access",
			"Building a projection requires configured provider instances to read the live system with, and none were given.",
		))
		return empty, diags
	}

	b := newBuilder(ctx, cfg, provs, opts)
	b.run(ctx, resolutions)

	for _, db := range opts.DeposedBindings {
		b.materializeDeposed(ctx, db)
	}

	sortOmissions(b.omissionList)
	sort.Slice(b.materialized, func(i, j int) bool {
		return b.materialized[i].String() < b.materialized[j].String()
	})

	sortUnowned(b.unownedList)
	sort.Slice(b.recordVersions, func(i, j int) bool {
		return b.recordVersions[i].Addr.String() < b.recordVersions[j].Addr.String()
	})
	sort.Slice(b.envelopeVersions, func(i, j int) bool {
		return b.envelopeVersions[i].Addr.String() < b.envelopeVersions[j].Addr.String()
	})
	sort.Slice(b.policyList, func(i, j int) bool {
		return b.policyList[i].Addr.String() < b.policyList[j].Addr.String()
	})

	res := &Result{
		cacheHits:        b.cacheHits,
		State:            b.state,
		Materialized:     b.materialized,
		Omitted:          b.omissionList,
		Unowned:          b.unownedList,
		RecordVersions:   b.recordVersions,
		EnvelopeVersions: b.envelopeVersions,
		Policy:           b.policyList,
	}
	// Issue #685: report the cache's effect, always, including zero. A cache
	// that is configured and never hits looks exactly like one that is working
	// unless the number is printed, and that indistinguishability is the whole
	// reason this fork shipped documentation describing a cache it did not
	// write.
	if opts.StateCache != nil {
		log.Printf("[DEBUG] projection: state cache supplied %d instance(s) that would otherwise have been read", b.cacheHits)
	}

	return res, diags.Append(b.diags)
}

// CacheHits reports how many instances this build answered from
// [Options.StateCache] instead of a provider read. Zero when no cache was
// given, and zero is also a legitimate answer for a cache that matched
// nothing the estate sweep verified.
func (r *Result) CacheHits() int { return r.cacheHits }

// newBuilder is the one place a builder is constructed, shared by
// [buildFrom] and by [ReadInstances] so that a narrow read talks to
// providers through exactly the same cache, the same identity check and the
// same `moved` set a full projection does. A second construction site here
// is how one of the two would come to read through a differently-configured
// provider than the other.
func newBuilder(ctx context.Context, cfg *configs.Config, provs Providers, opts Options) *builder {
	// The config-side naming signal, for the identity check the provider
	// cache runs when the schemas arrive (schema_check.go). Its diagnostics
	// are dropped on purpose: a configuration whose expansion this cannot
	// enumerate is a resolution failure the caller has already seen, and a
	// second copy of it here would fail a projection over a report.
	signal, _ := identity.ScanConfig(ctx, cfg)

	return &builder{
		cfg:                  cfg,
		opts:                 opts,
		providers:            newProviderCache(provs, signal),
		state:                states.NewState(),
		live:                 make(map[string]cty.Value),
		omitted:              make(map[string]Omission),
		causes:               make(map[string]string),
		depsByType:           make(map[string][]addrs.ConfigResource),
		envelopeVersionAddrs: make(map[string]bool),
		materializedIdentity: make(map[string]bool),
		// The `moved` blocks this configuration's markers may follow (GitHub
		// issue #198), computed once for the whole projection because
		// [builder.checkOwnership] asks about them per instance. A
		// configuration with no moved blocks gets an empty slice here and
		// [moved.Accepts] degenerates to one address comparison.
		movedStmts: moved.Honoured(cfg),
	}
}

type builder struct {
	cfg       *configs.Config
	opts      Options
	providers *providerCache

	// movedStmts is the honoured `moved` statements of cfg, which decide
	// which prior tofu-address markers still name a declared instance. See
	// [builder.checkOwnership].
	movedStmts []moved.Statement

	state *states.State

	// live holds the materialized object value of each instance already in
	// the projection, keyed by address string. It is what a parent-derived
	// formula reads its parents' live IDs out of.
	live map[string]cty.Value

	omitted      map[string]Omission
	omissionList []Omission
	materialized []addrs.AbsResourceInstance
	unownedList  []Unowned
	policyList   []PolicyOutcome

	// materializedIdentity records, for every instance this pass has
	// successfully materialized, whether SOME instance of its resource type
	// carries its import identity - GitHub issue #404. Keyed by
	// "<type>\x00<import ID>" rather than by address on purpose: two
	// addresses in the same plan can both name the exact same live object
	// (an untaggable, single-parent-component child of a resource a `moved`
	// block or ordinary parent-derived re-discovery relocated, still
	// carrying an [identity.Resolution.Undeclared] entry at its OLD
	// address from the estate's own record store - see [builder.run]'s own
	// doc comment on the two-part concrete phase for why that entry exists
	// at all), and this is what lets the second part tell "a genuine
	// removal" from "the same object, claimed twice."
	materializedIdentity map[string]bool

	// recordVersions is the version read at plan time for every
	// record-backed instance whose record actually existed - GitHub issue
	// #73's write-back needs it to open PutIfVersion/Delete with the right
	// expected version. An instance with no prior record (about to be
	// created) has no entry here, which write-back reads as expectedVersion
	// "" - a create assertion, exactly [staterecord.Store]'s own convention.
	recordVersions []RecordVersion

	// envelopeVersions is GitHub issue #364's merge of what used to be
	// three separate fields (locatedVersions, residueVersions,
	// provisionedVersions) for GitHub issues #270, #275 and #353: the
	// version read at plan time for every kind=identity envelope that
	// actually existed, from whichever of materializeLocated,
	// fillResidueFor or applyProvisionedTaint happened to read it first for
	// a given address. See [Result.EnvelopeVersions] for why one list is
	// correct now that the three concerns share one physical key.
	//
	// Populated only through [builder.recordEnvelopeVersion], never by a
	// direct append: since the three concerns share one key, materializing
	// a located instance can have all three read the identical key/version
	// pair in the same pass (identity for materializeLocated, then residue
	// and provisioned again for the ordinary materialize() call it makes) -
	// recordEnvelopeVersion is what keeps that one physical fact from
	// entering write-back's expected-version list two or three times over.
	envelopeVersions     []RecordVersion
	envelopeVersionAddrs map[string]bool

	// causes holds a short subordinate clause per omitted instance, for
	// use inside another instance's explanation. Omission.Detail is a
	// standalone sentence and reads badly nested inside one.
	causes map[string]string

	// depsByType caches the config-level dependency set per resource
	// block, since every instance of a resource shares one.
	depsByType map[string][]addrs.ConfigResource

	// ambientByProvider is GitHub issue #402's cross-instance memory: the
	// account id and region [ambientIdentityValues] reads off ANY
	// instance's own resource-identity object, keyed by provider config
	// address, so a sibling type whose Read echoes the identical ambient
	// value into a deprecated argument but never itself returns a native
	// identity (aws_s3_bucket_object_lock_configuration, confirmed against
	// floci: its own read carries no [providers.ReadResourceResponse]
	// NewIdentity at all, unlike its cors/versioning/server_side_encryption
	// siblings, even though its Read echoes the identical account id) is
	// not left unguarded just because IT never served the evidence.
	//
	// This is not a guess and not new plumbing from outside the package:
	// every value that ever enters it came from a real read through THIS
	// SAME provider connection, earlier in this SAME run - see
	// [builder.ambientContext].
	ambientByProvider map[string]map[string]cty.Value

	// readPrefetch is the current phase's in-flight reads, GitHub issues #585
	// and #654, and is non-nil only for the duration of a phase that starts
	// one: [builder.applyRecordFirst]'s record-first intercept, then
	// [builder.run]'s own concrete loop. The two never overlap - the intercept
	// finishes and nils this field before run reaches orderWork.
	// [builder.readFor] consults it; the derived, located and undeclared loops
	// start none, find it nil, and read inline exactly as every phase did
	// before it existed.
	readPrefetch *readPrefetch

	// readWasted and readMismatched are [readPrefetch.finish]'s and
	// [readPrefetch.mismatches]' answers, accumulated across every phase that
	// starts a prefetch: instances whose read was prefetched and never
	// consumed, and answers a consumer declined because the plan named a
	// different resolution. Both are always
	// zero - a non-zero either way is a provider round trip the sequential
	// pass would not have made, which is the property issue #585 accepts on -
	// and they are recorded rather than asserted here so a real run degrades
	// into one extra read rather than a panic.
	readWasted     []string
	readMismatched int

	// cacheHits counts instances answered from Options.StateCache instead of a
	// provider read. Reported so a run can PROVE the cache was used rather
	// than merely configured.
	cacheHits int

	diags tfdiags.Diagnostics
}

// ambientContext is [scrubAmbientEcho]'s ambient input, GitHub issue #402:
// this instance's own resource identity, merged into and then read back
// from [builder.ambientByProvider] so an instance whose OWN read carries no
// native identity still benefits from what a sibling instance through the
// identical provider connection already proved. Merging before reading
// keeps a single-instance estate (nothing else to learn from) exactly as
// safe as a multi-instance one: this instance's own evidence, if it has
// any, is always in the map by the time this function returns it.
func (b *builder) ambientContext(providerAddr addrs.AbsProviderConfig, schema providers.Schema, identityObj cty.Value) map[string]cty.Value {
	own := ambientIdentityValues(schema, identityObj)
	key := providerAddr.String()
	if len(own) > 0 {
		if b.ambientByProvider == nil {
			b.ambientByProvider = make(map[string]map[string]cty.Value)
		}
		cached := b.ambientByProvider[key]
		if cached == nil {
			cached = make(map[string]cty.Value, len(own))
		}
		for name, v := range own {
			cached[name] = v
		}
		b.ambientByProvider[key] = cached
	}
	return b.ambientByProvider[key]
}

func (b *builder) run(ctx context.Context, resolutions []identity.Resolution) {
	// GitHub issue #404: every [identity.Resolution.Undeclared] concrete
	// resolution is pulled out before anything else runs - including
	// [applyRecordFirst] - and held for a final pass at the bottom of this
	// function. See that pass's own doc comment for the full mechanism;
	// the reason it has to happen THIS early, ahead of applyRecordFirst
	// rather than only ahead of orderWork's concrete/derived split, is
	// that applyRecordFirst reads the SAME record store an undeclared
	// resolution's own source (recordOrphanReadSweep, or any future
	// record-store-sourced orphan leg) just read to produce it: an
	// undeclared entry always has a record there by construction, so
	// applyRecordFirst.materializeFromRecord would otherwise materialize
	// it immediately, before a single currently-declared instance - which
	// only ever runs through applyRecordFirst/orderWork's later phases -
	// has had the chance to claim the same import identity. Splitting
	// this out is safe for every OTHER resolution's own dependency
	// ordering: nothing in the CURRENT configuration can reference an
	// address the configuration does not declare, so an undeclared
	// instance is never a formula parent for anything still running
	// through the phases below it.
	var undeclaredConcrete []identity.Resolution
	rest := make([]identity.Resolution, 0, len(resolutions))
	for _, r := range resolutions {
		if r.Undeclared && r.Class == identity.ClassConcrete {
			undeclaredConcrete = append(undeclaredConcrete, r)
			continue
		}
		rest = append(rest, r)
	}

	rest = b.applyRecordFirst(ctx, rest)
	concrete, derived, needsDiscovery, cyclic, recordBacked, located := orderWork(rest)

	for _, r := range needsDiscovery {
		b.omit(r.Addr, ReasonNeedsDiscovery, needsDiscoveryDetail(r), needsDiscoveryCause(r))
	}

	declaredRecordBacked := make(map[string]bool, len(recordBacked))
	for _, r := range recordBacked {
		declaredRecordBacked[r.Addr.String()] = true
		b.materializeRecord(ctx, r.Addr, false)
	}
	b.discoverOrphanedRecords(ctx, declaredRecordBacked)

	// GitHub issue #270's located instances run before the concrete ones so
	// that a parent-derived formula naming a located parent finds it in
	// b.live by the time the derived phase reads it - the same reason
	// concrete runs before derived. There is deliberately no
	// discoverOrphanedRecords counterpart: nothing enumerates the located
	// namespace, which is the point of it.
	for _, r := range located {
		b.materializeLocated(ctx, r.Addr)
	}

	for _, r := range cyclic {
		detail := fmt.Sprintf(
			"The identities of %s and the instances it derives from refer to each other in a cycle, so there is no order in which they can be read. This is a bug in identity resolution: a parent-derived identity must name parents that are resolvable first.",
			r.Addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cyclic parent-derived identities", detail))
		b.omit(r.Addr, ReasonCycle, detail, "its identity formula is part of a cycle and can never be rendered.")
	}

	// GitHub issue #585: the concrete phase is the read pass's bulk - every
	// instance whose identity this run already holds, which after discovery
	// has bound the marker-carrying ones is nearly all of them - and each
	// instance's ImportResourceState/ReadResource pair is independent of every
	// other's. The list is built first so that the plan and the loop are
	// driven from the SAME values rather than from two constructions of them,
	// and so the two can never disagree about what is being read.
	//
	// The loop below is unchanged. [builder.startReadPrefetch] moves the
	// waiting and nothing else: the same calls, with the same arguments, in
	// the same order, consumed by the same body one instance at a time.
	concreteWanted := make([]wanted, 0, len(concrete))
	for _, r := range concrete {
		concreteWanted = append(concreteWanted, wanted{
			addr:       r.Addr,
			importID:   r.ImportID,
			identity:   r.Identity,
			values:     r.IdentityValues,
			undeclared: r.Undeclared,
		})
	}
	b.readPrefetch = b.startReadPrefetch(ctx, concreteWanted)
	for _, w := range concreteWanted {
		b.materialize(ctx, w)
	}
	b.readWasted = append(b.readWasted, b.readPrefetch.finish()...)
	b.readMismatched += b.readPrefetch.mismatches()
	b.readPrefetch = nil

	for _, r := range derived {
		id, values, ok := b.renderFormula(r)
		if !ok {
			continue
		}
		b.materialize(ctx, wanted{
			addr:       r.Addr,
			importID:   id,
			identity:   r.Identity,
			values:     values,
			undeclared: r.Undeclared,
		})
	}

	// GitHub issue #404: undeclared, concrete resolutions - pulled out at
	// the very top of this function, before applyRecordFirst ever saw
	// them - are materialized last, now that every currently-declared
	// instance (concrete, and derived by a parent-derived formula alike)
	// has had its chance to claim an import identity, recorded in
	// [builder.materializedIdentity]. An untaggable, single-parent-
	// component child of a resource a `moved` block, or ordinary
	// parent-derived re-discovery, relocated re-resolves the SAME live
	// object at its NEW, declared address without needing any `moved`
	// statement of its own - the same property day2_rename's own e2e
	// header comment documents - while its OLD address's identity record,
	// never itself moved or deleted, sits in the estate's record store
	// exactly as recordOrphanReadSweep (GitHub issue #364 ruling item 1)
	// left it and would otherwise still read as a genuine removal. Without
	// this check, that old address plans a destroy in the SAME run that
	// keeps the object alive under its new one - a live, still-owned
	// object proposed for destruction, which HANDOFF.md's safety rule
	// treats as worse than a missing marker. A genuine removal (nothing
	// currently declared claims the same import identity) is unaffected:
	// materializedIdentity has no entry for it, and it materializes here
	// exactly as it always has, still destined for the ordinary
	// undeclared-instance destroy.
	for _, r := range undeclaredConcrete {
		if r.ImportID != "" && b.materializedIdentity[r.Addr.Resource.Resource.Type+"\x00"+r.ImportID] {
			b.omit(r.Addr, ReasonSuperseded, fmt.Sprintf(
				"%s is not in the configuration, but the live object it names (import identity %q) is the SAME object a currently-declared instance of the same type already claimed earlier in this plan. It was relocated - by a `moved` block, or by ordinary parent-derived re-discovery of a renamed parent - rather than removed, and the declared instance is what will keep managing it. Nothing is proposed for this address.",
				r.Addr, r.ImportID,
			), "the same live object is already claimed by a currently-declared instance elsewhere in this plan, so this old address is superseded rather than orphaned.")
			continue
		}
		b.materialize(ctx, wanted{
			addr:       r.Addr,
			importID:   r.ImportID,
			identity:   r.Identity,
			values:     r.IdentityValues,
			undeclared: r.Undeclared,
			dependsOn:  r.DestroyDependsOn,
			// See [identity.Resolution.RecordRooted]'s doc comment and
			// [wanted.located]'s: an undeclared resolution sourced from the
			// record store (recordOrphanReadSweep today) is owned proof
			// the same way a declared ClassRecordLocated instance is, and
			// must be trusted unconditionally rather than routed through
			// checkOwnership's ordinary taggable-type tag check, which
			// would find no tag - by the same markers=record selection
			// that put the identity in the record instead - and silently
			// omit a real, correctly-identified object instead of
			// proposing its destroy.
			located: r.RecordRooted,
		})
	}

	// gauntlet:destroy-order (corpus-autoscaling-complete's day2_remove
	// unit): a reference edge BETWEEN two sibling undeclared orphans, with
	// no HCL left on either side to read it from. See
	// [builder.deriveUndeclaredReferenceEdges]'s own doc comment for why
	// this has to run after every undeclaredConcrete instance above has
	// materialized rather than being folded into [wanted.dependsOn] up
	// front: the values being compared are each OTHER sibling's own live
	// read, and this batch's own ordering (map iteration inside
	// [classifyOrphans], not a guaranteed one) cannot be trusted to have
	// read the far side first.
	b.deriveUndeclaredReferenceEdges(undeclaredConcrete)
}

// deriveUndeclaredReferenceEdges is the mirror, for two sibling undeclared
// orphans, of what [builder.dependencies] reads off a declared instance's
// own configuration references, and of what [destroyParentDependency]
// (internal/live/discovery/recordorphan_read.go) reads off one record's own
// named component for the narrower PARENT/child case. Neither instance in
// an undeclared pair has a resource block left to read a reference from,
// and [identity.ParentOf] only ever links a child to its identity-deriving
// parent - a narrower relation than "one resource's live object names
// another's own identity somewhere in its attributes."
//
// Found building corpus-autoscaling-complete's day2_remove unit:
// aws_autoscaling_group and aws_launch_template are sibling undeclared
// orphans with no parent/child link between them at all (an ASG's launch
// template is a plain attribute reference, not an identity component ASG's
// own table derives from), and destroying the template first - the order
// nothing in this pass otherwise constrains - fails against the real API
// with "ValidationError: The specified launch template does not exist."
// Stock never hits this: its state file remembers the ASG referenced the
// template from the original apply and reverses that for the destroy,
// which is exactly what this function reconstructs with no state file to
// read it back from.
//
// resolved is the batch [builder.run] just finished materializing through
// undeclaredConcrete: every genuinely undeclared, tag-found orphan in this
// one plan. That is deliberately the whole comparison scope, the same
// scope [destroyParentDependency] and [builder.discoverOrphanedRecords]
// both stay inside for the identical reason - a currently DECLARED
// resource can never reference something this pass is about to destroy,
// since its own configuration would have nothing to evaluate the reference
// against, so widening the comparison to every materialized address would
// only add false edges, never recover a missing one.
//
// The match is generic on purpose: never a concrete aws_* type name
// anywhere in this function, because the question is not "is this an ASG
// and that a launch template" - it is "does this object's own live value,
// anywhere in its structure, hold ANOTHER sibling's identity string."
// [identity.Resolution.ImportID] is exactly that string, the same one
// [destroyParentDependency] already compares by == for the narrower
// parent/child case; here it is looked for inside the WHOLE sibling
// object rather than inside one named record component, via
// [containsStringValue]'s generic walk.
//
// ANOTHER's is load-bearing, found running this exact fix against
// corpus-security-group-complete's day2_remove unit in the same session,
// in two shapes that share one root cause:
// aws_vpc_security_group_rules_exclusive's whole identity IS its security
// group's own id (identity.Component.IdentityAttr: "*" over
// security_group_id, table_generated.go), so a naive from-contains-to
// scan is symmetric wherever it fires on this pair: comparing the
// security group's own live value against rules_exclusive's ImportID
// matches (the security group's own id, trivially, since a live value
// always contains its own id), and the SAME scan the other way ALSO
// matches (rules_exclusive's own security_group_id attribute is that
// identical string). Two candidate edges between the same two nodes,
// pointing opposite ways, is not evidence of a reference in either
// direction - it is two objects answering to one identity string - and it
// cycled against [destroyParentDependency]'s own, correctly-directed
// rules_exclusive-depends-on-the-security-group edge for the identical
// pair (a SEPARATE mechanism, over structured identity Components rather
// than raw live-value scanning, and unaffected by anything below).
//
// The second shape is the one a same-pair check alone misses: every
// ingress/egress rule ALSO matches rules_exclusive symmetrically, for a
// DIFFERENT reason on each side. The rule's own security_group_id
// attribute names the security group, which happens to equal
// rules_exclusive's ImportID (the shape above) - forward direction. And
// aws_vpc_security_group_rules_exclusive's own required arguments,
// `ingress_rule_ids`/`egress_rule_ids` (its own live value, confirmed
// against the provider's docs), are themselves lists of the EXACT rule
// ids this pass is trying to order - backward direction. Both fire for
// every rule in the batch, so the SCC search in the plan graph merges
// every rule that shares this shape with rules_exclusive into one
// four-node cycle, with no security group in it at all. It is exactly as
// spurious as the direct case: the provider's own docs are explicit that
// destroying rules_exclusive makes no AWS call at all ("Terraform will no
// longer manage reconciliation... it will not revoke the configured
// rules"), so there is no real ordering constraint between it and the
// rules it names either way.
//
// Both shapes are the same generic fact under one rule: a candidate match
// found in BOTH directions between two siblings - from contains to's id,
// AND to contains from's id - is not a directed reference at all; it is
// two objects mutually restating each other's identity, and neither
// direction is kept. What survives is exactly the one-directional shape a
// genuine reference takes (an ASG names its launch template; nothing in
// the launch template's own live value names the ASG back), which is why
// this has to be a two-pass computation: every candidate in the batch is
// found first, then only the ones with no reverse candidate are kept.
func (b *builder) deriveUndeclaredReferenceEdges(resolved []identity.Resolution) {
	type sibling struct {
		addr     addrs.AbsResourceInstance
		importID string
	}
	var siblings []sibling
	for _, r := range resolved {
		if r.ImportID == "" {
			// No identity string to look for elsewhere, and nothing this
			// resolution's own object could be found holding either -
			// [identity.Resolution.Identity]-only instances have no such
			// string at all; see [wanted.importID]'s own doc comment.
			continue
		}
		if _, ok := b.live[r.Addr.String()]; !ok {
			// Not materialized this pass - omitted, absent, or superseded
			// (see [builder.run]'s own undeclaredConcrete loop just above)
			// - so there is nothing to scan and nothing to depend on.
			continue
		}
		siblings = append(siblings, sibling{addr: r.Addr, importID: r.ImportID})
	}
	if len(siblings) < 2 {
		return
	}

	// Pass 1: every candidate edge in the batch, indexed by the pair of
	// positions in siblings so pass 2 can check the reverse cheaply.
	type pair struct{ from, to int }
	candidates := make(map[pair]bool)
	for i, from := range siblings {
		val, ok := b.live[from.addr.String()]
		if !ok {
			continue
		}
		for j, to := range siblings {
			if i == j {
				continue
			}
			if containsStringValue(val, to.importID) {
				candidates[pair{i, j}] = true
			}
		}
	}

	// Pass 2: keep a candidate only when its reverse is not also a
	// candidate - see this function's own doc comment for why a mutual
	// match is never a directed reference.
	deps := make(map[int][]addrs.ConfigResource, len(siblings))
	for p := range candidates {
		if candidates[pair{p.to, p.from}] {
			continue
		}
		deps[p.from] = append(deps[p.from], siblings[p.to].addr.ConfigResource())
	}
	for i, ds := range deps {
		b.addStateDependencies(siblings[i].addr, ds)
	}
}

// addStateDependencies merges extra into addr's already-encoded state
// entry's own Dependencies field, deduplicated and sorted the same way
// [builder.materialize]'s own w.dependsOn branch merges [wanted.dependsOn]
// - the identical merge, applied after the fact, because
// [builder.deriveUndeclaredReferenceEdges] cannot know what to merge in
// until every sibling in its batch has already been read and written into
// the projection. [states.ResourceInstanceObjectSrc.Dependencies] is a
// plain Go field, never part of the encoded attributes, so mutating it
// post-hoc changes nothing else about the already-written object.
//
// A silent no-op when addr is not in the state at all (should not happen
// for an address [builder.deriveUndeclaredReferenceEdges] already found in
// b.live, which is only ever populated alongside a state write) or has no
// Current object - there is nothing this function could be attaching a
// destroy-order hint to.
func (b *builder) addStateDependencies(addr addrs.AbsResourceInstance, extra []addrs.ConfigResource) {
	mod := b.state.Module(addr.Module)
	if mod == nil {
		return
	}
	inst := mod.ResourceInstance(addr.Resource)
	if inst == nil || inst.Current == nil {
		return
	}
	seen := make(map[string]addrs.ConfigResource, len(inst.Current.Dependencies)+len(extra))
	for _, cr := range inst.Current.Dependencies {
		seen[cr.String()] = cr
	}
	for _, cr := range extra {
		seen[cr.String()] = cr
	}
	deps := make([]addrs.ConfigResource, 0, len(seen))
	for _, cr := range seen {
		deps = append(deps, cr)
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].String() < deps[j].String() })
	inst.Current.Dependencies = deps
}

// containsStringValue reports whether obj holds target as a string leaf
// anywhere in its structure: a plain top-level attribute, or nested inside
// a block, list, set or map - exactly the shape an AWS provider's own
// nested "launch_template { id = ... }" attribute takes, without this
// function (or its one caller, [builder.deriveUndeclaredReferenceEdges])
// ever naming that attribute or the type it belongs to.
//
// A marked (sensitive or ephemeral) value is skipped rather than unmarked:
// HANDOFF's safety rule about a mark-unsafe cty accessor
// (internal/live/marksafe) applies here exactly as it does to an identity
// or a tag, even though this result only ever feeds a destroy-order hint
// and never an identity component or a cloud tag - the guard costs nothing
// and keeps this function inside the same discipline as every other cty
// read in this package.
func containsStringValue(obj cty.Value, target string) bool {
	if target == "" || obj == cty.NilVal {
		return false
	}
	found := false
	_ = cty.Walk(obj, func(_ cty.Path, v cty.Value) (bool, error) {
		if found {
			return false, nil
		}
		if v.IsMarked() {
			// Refuse rather than unmark: see this function's own doc
			// comment. Not descending is also correct on its own terms -
			// an object identity string sitting behind a sensitivity mark
			// is not a shape this fork's schema-driven marking ever
			// produces for an ordinary computed attribute like an id.
			return false, nil
		}
		if v.IsNull() || !v.IsKnown() {
			return true, nil
		}
		if v.Type() == cty.String && v.AsString() == target {
			found = true
			return false, nil
		}
		return true, nil
	})
	return found
}

// applyRecordFirst is GitHub issue #364 unit B's read half: rulings/20260823-
// foundation-order-ruling.md's ruling 1 and HANDOFF.md's "The foundation"
// ("Binding reads the record and verifies it against the marker") applied
// ahead of [orderWork], so that a resolution's identity.Class no longer
// decides whether the estate's own record store gets consulted at all.
//
// Every resolution not already produced by the record store's own two
// existing doors (identity.ClassRecordBacked, which has no cloud object for
// a record to name; identity.ClassRecordLocated, which already reads and
// trusts - or, for an operator's `markers = record` selection, deliberately
// never verifies - the identical record through [builder.materializeLocated])
// gets one attempt at [builder.materializeFromRecord]. An attempt that fully
// answers the question - the instance materialized, or was terminally
// omitted as absent, failed, or unowned - drops the resolution from what
// [orderWork] still has to route. An attempt that finds no record, or finds
// one but the object it names turns out stale (see
// [builder.materializeFromRecord]), changes nothing: the resolution goes on
// to [orderWork] exactly as it arrived, and takes whatever path its
// identity.Class would have taken with no record store in play at all.
//
// GitHub issue #654: this loop, not the concrete phase below it, is where a
// MIGRATED estate does its reading. Every instance an apply or a migration has
// written a record for is intercepted here, and after issue #636 made the
// store one GetAll the interception is free, so on the estate this fork exists
// to run it catches nearly everything: 78 of 79 instances at scale 1 on the
// terralith, leaving the concrete phase with one. Issue #585 gave the concrete
// phase a prefetch and left this loop reading one instance at a time, which is
// why a real-AWS plan of 745 resources spent 124 seconds against stock's 22-39
// for the same 1399 calls - measured serial, one request in flight, start to
// finish. The prefetch is started here for exactly the same reason and in
// exactly the same shape: the loop below is unchanged, consuming the same
// answers to the same calls in the same order.
func (b *builder) applyRecordFirst(ctx context.Context, resolutions []identity.Resolution) []identity.Resolution {
	if b.opts.RecordStore == nil {
		return resolutions
	}
	b.readPrefetch = b.startRecordFirstPrefetch(ctx, resolutions)
	remaining := make([]identity.Resolution, 0, len(resolutions))
	for _, r := range resolutions {
		switch r.Class {
		case identity.ClassRecordBacked, identity.ClassRecordLocated:
			remaining = append(remaining, r)
			continue
		}
		if b.materializeFromRecord(ctx, r) {
			continue
		}
		remaining = append(remaining, r)
	}
	b.readWasted = append(b.readWasted, b.readPrefetch.finish()...)
	b.readMismatched += b.readPrefetch.mismatches()
	b.readPrefetch = nil
	return remaining
}

// materializeFromRecord is [builder.applyRecordFirst]'s attempt at one
// resolution. It reports whether the address is fully handled.
//
// No record at all is not a failure and not reported: it is the ordinary
// shape for an estate that has not migrated, or for an instance an apply
// has never written back, and returning false here sends the resolution
// through [orderWork] with nothing said about it.
//
// A record that exists is handed to [builder.materialize] as an ordinary
// [wanted], exactly the way [builder.materializeLocated] already does for
// identity.ClassRecordLocated - the same import, the same read, the same
// dependency computation - except recordFirst is set rather than located,
// which is what makes [builder.checkOwnership] verify a taggable type's
// binding against the live object's own marker instead of trusting it
// outright. A false result from [builder.materialize] means that check
// found the record stale; [builder.checkOwnership] has already logged the
// warning, and this function reports the address as unhandled with nothing
// further to say.
func (b *builder) materializeFromRecord(ctx context.Context, r identity.Resolution) bool {
	rec, version, keyExists, identityFound, err := b.opts.RecordStore.GetIdentity(ctx, r.Addr)
	if err != nil {
		detail := fmt.Sprintf("Reading the record for %s failed: %s.", r.Addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read a persisted record", detail))
		b.omitFailed(r.Addr, detail)
		return true
	}
	if !identityFound {
		return false
	}
	if keyExists {
		b.recordEnvelopeVersion(r.Addr, version)
	}
	return b.materialize(ctx, wanted{
		addr:        r.Addr,
		importID:    rec.ImportID,
		values:      recordFirstStubValues(rec),
		undeclared:  r.Undeclared,
		recordFirst: true,
	})
}

// recordFirstStubValues is [builder.materializeFromRecord]'s GitHub issue
// #401 family 1 half: it merges rec.ImportID onto rec.Components under the
// schema's own "id" attribute name, when both are already present, so the
// stub [noimporter.SynthesizeStub] builds for a type with no classic
// Importer (importAndRead, reached through the noimporter.Diagnostics
// signal) can carry an "id" the way every genuine ReadResource PriorState
// already does - an ordinary refresh's own stub, or one ImportResourceState
// itself built.
//
// "id" is never among rec.Components: SynthesizeStub places a value only
// under a name an identity.Component actually resolved, and a type's "id"
// is the provider's own opaque state key, never an identity-schema
// attribute (see [identity.SynthesizeTypeIdentity]'s own doc comment on
// deliberately never adding "id" to a synthesized entry's IdentityAttrs).
// So a stub built from Components alone can structurally never carry one,
// which for a type like aws_acm_certificate_validation is the one
// difference between the stub this run can build and the PriorState a
// real refresh sends - and rec.ImportID already holds exactly that value,
// transcribed once from the same real prior object the components
// themselves came from ([schemaFallbackComponentsRecord], its writer).
//
// A copy, never a mutation of rec.Components: the record this run just
// read may be reused or cached elsewhere, and adding "id" to it is a fact
// about this one stub-building attempt, not about the record.
//
// rec.Components empty - today's shape for every record this mechanism
// does not yet reach - returns it unchanged (nil or empty, same as before
// this function existed): [noimporter.SynthesizeStub]'s own
// len(values)==0 refusal is exactly as load-bearing as it was, and this
// never manufactures a values map where there was none. rec.ImportID
// empty is the record-backed/composite-only shape that never carries a
// separate id string at all, and is likewise left untouched.
func recordFirstStubValues(rec LocatedRecord) map[string]string {
	if rec.ImportID == "" || len(rec.Components) == 0 {
		return rec.Components
	}
	values := make(map[string]string, len(rec.Components)+1)
	for k, v := range rec.Components {
		values[k] = v
	}
	values["id"] = rec.ImportID
	return values
}

// wanted is one instance's identity in every form this run holds it, which is
// the input [builder.materialize] works from.
//
// The forms are not alternatives to choose between up front. The string is
// what every operator-facing line prints and what a marker rewrite records,
// so it is always carried; whether either identity form can be used at all is
// a question about the provider's schema, which is not known until a plugin
// is on the line. So all of them travel here and [importTarget] decides per
// resource, once the schema has arrived.
type wanted struct {
	addr addrs.AbsResourceInstance

	// importID is the provider's import-ID string.
	//
	// Empty for a type identified by several attributes with no separator
	// between them: there is no such string, and inventing one is what
	// GitHub issue #105 exists to prevent. See
	// [identity.TypeIdentity.IdentityObjectOnly]. Such an instance is
	// imported by identity object or refused, never by an approximation.
	importID string

	// identity is the provider's own resource identity object, when
	// something served one: a marker sweep's list results carry it. Null
	// otherwise.
	identity cty.Value

	// values is the identity the configuration supplies, one string per
	// identity attribute, which is importID unjoined. Nil for a type whose
	// entry does not say which attribute each component feeds. See
	// [identity.Resolution.IdentityValues].
	values map[string]string

	undeclared bool

	// dependsOn is [identity.Resolution.DestroyDependsOn], carried through
	// unchanged: the ordering hint an undeclared instance's own discovery
	// leg supplied in place of the configuration reference
	// [builder.dependencies] would ordinarily read. See that field's own
	// doc comment for why an undeclared instance needs one at all. Nil for
	// every declared instance (rc != nil in [builder.materialize] already
	// computes its own, real dependency set from configuration) and for
	// most undeclared ones (no leg has supplied one).
	dependsOn []addrs.AbsResourceInstance

	// located records that this instance's identity came out of the
	// estate's located record store (identity.ClassRecordLocated) rather
	// than from the configuration or from a marker sweep. See
	// [builder.materializeLocated], and [builder.checkOwnership] for the one
	// decision that turns on it. Its identity is trusted unconditionally
	// once the object is found - either because the type has nowhere to
	// carry a marker at all, or because an operator's `markers = record`
	// selection deliberately traded marker governability for record
	// authority (GitHub issue #365) - which is exactly the reason
	// [recordFirst] below is a separate field rather than a second meaning
	// for this one.
	located bool

	// recordFirst marks GitHub issue #364 unit B's universal read: this
	// instance's identity came out of the estate's record store ahead of
	// its identity.Class's own routing (see [builder.applyRecordFirst]),
	// for a type that ordinarily derives its identity some other way. Unlike
	// [located], a recordFirst binding is not trusted unconditionally: for a
	// type that CAN carry a marker, [builder.checkOwnership] verifies the
	// live object's own tofu-address against this address before admitting
	// it, and a mismatch (or no marker at all) makes the record stale -
	// [builder.materializeFromRecord] then reports the instance as
	// unhandled, and it falls back to the marker sweep or the static
	// evaluator exactly as if no record had existed. A type with nowhere to
	// carry a marker has nothing to check the record against and is trusted
	// exactly as [located] is.
	recordFirst bool
}

// importTarget picks the form this instance's import is asked in.
//
// There are three sources and they rank in this order:
//
//   - The provider's own identity object, when a list call served one. It is
//     the provider's account of what names this resource, unaltered.
//   - The identity the configuration supplies, attribute by attribute, when
//     it covers every attribute the provider requires for import. This is
//     the same information the import-ID string holds, minus the separator
//     characters that only the string has - and the separators are the half
//     of the identity table that no schema can back.
//   - The import-ID string. Not a lesser answer: it is the only form
//     available for a type the provider serves no identity schema for, and
//     the only one for a type whose identity is something the configuration
//     does not hold (aws_route_table_association).
//
// The first two are exclusive with the third on the wire:
// [providers.ImportTarget.IsIdentityBased] decides, and both plugin protocols
// error rather than falling back when they are handed an identity for a type
// with no identity schema. So the choice is made here, where the schema is in
// hand, and exactly one field is set.
func importTarget(w wanted, schema providers.Schema) providers.ImportTarget {
	byID := providers.ImportTarget{ID: w.importID}

	if schema.IdentitySchema == nil {
		return byID
	}

	if w.identity != cty.NilVal && !w.identity.IsNull() {
		val, err := convert.Convert(w.identity, schema.IdentitySchema.ImpliedType())
		if err == nil && !val.IsNull() && val.IsWhollyKnown() {
			return providers.ImportTarget{Identity: val}
		}
		// The provider served an identity its own schema does not describe,
		// which is a provider bug rather than anything this run can act on.
		// The import ID came off the same list result, so there is a working
		// answer and no reason to fail.
		log.Printf("[WARN] projection: %s came back with an identity that does not fit %s's identity schema (%v); falling back",
			w.addr, w.addr.Resource.Resource.Type, err)
	}

	if val, ok := identityFromValues(w, schema); ok {
		return providers.ImportTarget{Identity: val}
	}

	// The fallback is the string, and for a type that has none there is no
	// fallback to take. byID is {ID: ""}, which [importAndRead] refuses
	// rather than sending to a provider - GitHub issue #105's point exactly:
	// the danger was never an empty ID, it was a plausible one. A
	// separator-less composite whose values were joined anyway would arrive
	// here as "prodsvc" and be imported against a real account.
	if w.importID == "" {
		log.Printf("[WARN] projection: %s is identified by identity object only and one could not be built from configuration; there is no import ID to fall back to",
			w.addr)
	}
	return byID
}

// identityFromValues builds an identity object out of what the configuration
// said, checked against the provider's identity schema rather than asserted.
//
// Two bars, and both are about the schema being the authority. Every
// attribute the provider requires for import has to be one the configuration
// supplied, because an identity missing a required attribute names nothing;
// and every attribute the configuration supplied has to be one the schema
// has, because a table entry that maps an argument onto an attribute this
// provider version does not carry is a stale inference and the import ID it
// also produced is the safe reading of it. Failing either drops to the
// string, which is what every run did before this existed.
//
// Optional attributes the configuration says nothing about are left null on
// purpose: in the AWS provider they are account_id and region, the context
// the provider fills in from its own configuration, and filling them in from
// here would be this package guessing which account a run is against - the
// thing [identity.CloudContext] exists to refuse.
func identityFromValues(w wanted, schema providers.Schema) (cty.Value, bool) {
	if len(w.values) == 0 {
		return cty.NilVal, false
	}
	body := schema.IdentitySchema

	vals := make(map[string]cty.Value, len(body.Attributes))
	for name, at := range body.Attributes {
		if at.NestedType != nil {
			// No AWS identity schema has one, and a string per attribute is
			// the only shape the identity table can express, so an identity
			// with structure in it is one this cannot build rather than one
			// to approximate.
			log.Printf("[TRACE] projection: %s's identity attribute %q has a nested type, which an identity built from configuration cannot fill; importing by ID %q",
				w.addr.Resource.Resource.Type, name, w.importID)
			return cty.NilVal, false
		}
		raw, supplied := w.values[name]
		if !supplied {
			if at.Required {
				log.Printf("[TRACE] projection: %s supplies no %q, which %s's identity schema requires; importing by ID %q",
					w.addr, name, w.addr.Resource.Resource.Type, w.importID)
				return cty.NilVal, false
			}
			vals[name] = cty.NullVal(at.Type)
			continue
		}
		val, err := convert.Convert(cty.StringVal(raw), at.Type)
		if err != nil {
			log.Printf("[WARN] projection: %s's %q is %q, which is not a %s; importing by ID %q",
				w.addr, name, raw, at.Type.FriendlyName(), w.importID)
			return cty.NilVal, false
		}
		vals[name] = val
	}
	for name := range w.values {
		if _, ok := body.Attributes[name]; !ok {
			log.Printf("[TRACE] projection: the identity table supplies %q for %s and the provider's identity schema has no such attribute; importing by ID %q",
				name, w.addr.Resource.Resource.Type, w.importID)
			return cty.NilVal, false
		}
	}
	return cty.ObjectVal(vals), true
}

// normalizeIdentityAttrs asks the provider that just materialized obj
// whether obj's own identity-bearing attributes would survive being
// resubmitted as a brand-new create, and adopts the provider's answer where
// they would not.
//
// GitHub issue #281. A rendered identity component answers "may I delete
// this", but it is also reused as ordinary prior-state, and from there it
// has to survive comparison against the SAME argument's value on every
// later plan. When a provider normalizes an argument before it ever reaches
// the wire - AWS Route 53 strips a record name's trailing dot at plan time,
// confirmed empirically against floci: a create-shaped PlanResourceChange
// answers "foo.example.com" for a config literal of "foo.example.com." -
// an identity built from the raw configuration string can bind the correct
// live object under the wrong spelling. Import succeeds regardless - the
// API accepts either form - so the object materializes, but its ReadResource
// answer can still carry whatever spelling the import happened to ask for
// rather than the provider's own canonical one, and the ordinary plan that
// follows compares that spelling against the config's own (normalized)
// rendering and proposes a forced replace, once per run, forever.
//
// This names no resource type anywhere. It works by feeding the provider
// exactly what it already told this run: PriorState is null (a synthetic
// create), Config is obj stripped of its purely-computed attributes (see
// configValue), and whatever PlannedState says for an identity-bearing
// attribute IS what that attribute canonicalizes to under this provider,
// because the provider - never this package - decides what its own create
// path does to a string.
//
// Scoped narrowly on purpose:
//
//   - Only identity-bearing attributes are ever candidates - the ones
//     [identity.TypeIdentity.Components] named for this instance (w.values's
//     keys), never every attribute obj carries. A component the
//     configuration built the import identity from is exactly the value
//     every later run keeps comparing against configuration, which is the
//     one place a normalization mismatch turns into a standing replace.
//   - Only a plain top-level string attribute the resource schema and the
//     identity table agree is the same attribute is ever a candidate. A
//     component whose identity attribute is not also a same-named schema
//     attribute (an inAttr composite built from several components, or a
//     nested type) is left exactly as ReadResource returned it.
//
// Deliberately NOT gated on whether obj's current value already disagrees,
// textually, with what the configuration wrote (w.values): agreement
// between the two is exactly what the broken case looks like. w.values is
// what built the import request in the first place, so when a provider
// preserves an identity-object import's own input verbatim - rather than
// overwriting it with ReadResource's fresh answer - obj and the
// configuration agree with EACH OTHER while both disagree with what the
// provider's own create path would normalize either of them to (issue
// #281's exact shape). One PlanResourceChange call covers every candidate
// attribute of one instance; an instance with no identity-bearing string
// attributes makes no call at all.
//
// Any provider error - a synthetic create-shaped plan that does not
// validate for reasons this question never touches - leaves obj exactly as
// ReadResource returned it. This can only fail closed: the worst outcome is
// the pre-#281 behavior for that one instance, never a value this package
// invented.
//
// The return value is the same information restated as plain strings, keyed
// by attribute name: what [builder.materialize] uses to bring the LOGGED
// identity - importID, built from the same raw values before any of this
// ran - back in step with the object it now actually describes, so an
// operator reading TF_LOG=trace sees the spelling this instance settled on
// rather than the one that turned out not to survive contact with the
// provider. Nil when nothing changed.
func (b *builder) normalizeIdentityAttrs(ctx context.Context, provider providers.Interface, schema providers.Schema, typeName string, w wanted, obj *states.ResourceInstanceObject) map[string]string {
	if obj == nil || len(w.values) == 0 || schema.Block == nil {
		return nil
	}
	if obj.Value == cty.NilVal || obj.Value.IsNull() || !obj.Value.IsWhollyKnown() {
		return nil
	}

	type candidate struct {
		name string
		live cty.Value
	}
	var candidates []candidate
	for name := range w.values {
		attrSchema, declared := schema.Block.Attributes[name]
		if !declared || attrSchema.NestedType != nil || attrSchema.Type != cty.String {
			continue
		}
		live := obj.Value.GetAttr(name)
		if !live.IsKnown() || live.IsNull() || live.IsMarked() {
			// A marked value - sensitive, in this codebase's only sense of
			// the word - never becomes an identity component or a log
			// line, the same rule [resolver.stringValue] already applies to
			// every OTHER source an identity is built from. Refused, not
			// unmarked: this instance's identity-bearing attribute is left
			// exactly as ReadResource returned it.
			continue
		}
		// Deliberately not gated on "does this already agree with what the
		// configuration wrote": that agreement is exactly what the broken
		// case looks like. w.values["name"] is the raw configuration
		// string, which is also what built the import request in the
		// first place - so when a provider preserves an identity-object
		// import's own input verbatim (rather than overwriting it with
		// ReadResource's fresh answer), obj's value and the configuration
		// agree with EACH OTHER while both disagree with what the
		// provider's own create path would normalize either of them to.
		// Checked once per instance below, never per attribute.
		candidates = append(candidates, candidate{name: name, live: live})
	}
	if len(candidates) == 0 {
		return nil
	}

	priorNull := cty.NullVal(schema.Block.ImpliedType())
	// Unmarked before it is put to the provider, and the unmark is the whole
	// of the proof that this call can be made at all. importAndRead marks
	// obj.Value from the schema, so for every type with a Sensitive attribute
	// - 61 of the 905 admitted types, by live/wo-sweep.json against
	// hashicorp/aws 6.59.0 - the value reaching here carries marks, and cty's
	// msgpack encoder refuses a marked value outright ("value has marks, so
	// it cannot be serialized"). The refusal arrives as an ordinary provider
	// diagnostic, which the error branch below turns into "left as
	// ReadResource returned them" - so GitHub issue #281's normalization was
	// silently off for every one of those types, with nothing but a TRACE
	// line to say so. It failed closed rather than wrongly, which is why it
	// was invisible; found while scouting #343, pinned by
	// TestNormalizeIdentityAttrsAsksTheProviderWithAnUnmarkedValue.
	//
	// Sending the unmarked value leaks nothing: it is the object this same
	// provider returned from its own ReadResource a few lines earlier. The
	// marks are re-derived from the schema, never from this round trip, and
	// the RESULT is filtered separately - a planned value that comes back
	// marked, and a candidate whose live value is marked, are both skipped
	// below.
	unmarkedObj, _ := obj.Value.UnmarkDeep()
	cfgVal := configValue(schema.Block, unmarkedObj)
	proposed := objchange.ProposedNew(schema.Block, priorNull, cfgVal)
	planResp := provider.PlanResourceChange(ctx, providers.PlanResourceChangeRequest{
		TypeName:         typeName,
		PriorState:       priorNull,
		ProposedNewState: proposed,
		Config:           cfgVal,
		// See importAndRead's identical call: a null of the dynamic
		// pseudo-type, never the zero cty.Value, or a provider that
		// declares a provider_meta schema panics its own conformance check.
		ProviderMeta: cty.NullVal(cty.DynamicPseudoType),
	})
	if planResp.Diagnostics.HasErrors() {
		log.Printf("[TRACE] projection: %s's identity-bearing attributes could not be checked against the provider's own create-time plan (%s); left as ReadResource returned them",
			w.addr, planResp.Diagnostics.Err())
		return nil
	}
	if planResp.PlannedState == cty.NilVal || planResp.PlannedState.IsNull() {
		return nil
	}

	updates := make(map[string]cty.Value, len(candidates))
	changed := make(map[string]string, len(candidates))
	for _, c := range candidates {
		planned := planResp.PlannedState.GetAttr(c.name)
		if !planned.IsKnown() || planned.IsNull() || planned.Type() != cty.String || planned.IsMarked() {
			continue
		}
		if c.live.IsMarked() {
			// Unreachable in practice - the candidate loop above already
			// filtered this out - but proven here too, at the point of
			// use, rather than trusted across the struct field it travels
			// through.
			continue
		}
		if planned.RawEquals(c.live) {
			continue
		}
		updates[c.name] = planned
		changed[c.name] = planned.AsString()
		log.Printf("[TRACE] projection: %s's %s normalized from %q to %q by the provider's own create-time plan; adopting the provider's spelling",
			w.addr, c.name, c.live.AsString(), planned.AsString())
	}
	if len(updates) == 0 {
		return nil
	}
	obj.Value = withReplacedAttrs(obj.Value, updates)
	return changed
}

// configValue is the live object as a configuration would express it: every
// attribute the provider alone can set is nulled, and everything else is
// carried across as it stands. It exists so that [builder.normalizeIdentityAttrs]
// can ask a provider "what would you store if this were a brand-new create",
// using the object the provider itself already returned as the config -
// mirroring internal/live/mv/rewrite.go's identical helper, which asks the
// same kind of question for a tags-only rewrite. Types are never altered,
// only values are nulled, so the result still conforms to the schema's
// implied type.
//
// No longer identical to those helpers, on purpose. Issue #373 widened the
// tag-write copies to null every Computed attribute rather than only the
// Computed-only ones, because a tags-only configuration asserts nothing but
// the tags. This one asks a different question: it is a synthetic CREATE
// against a null prior, and its whole job is to hand the provider the
// identity-bearing arguments a configuration WOULD have written so the
// provider can answer with its own canonical spelling of them. Many of those
// - a name, a domain, a record - are optional+computed, so nulling them here
// would replace the answer with an unknown and quietly retire GitHub issue
// #281's normalization. Whatever this returns also decides a rendered
// identity, which HANDOFF's safety rule puts behind by-value assertion
// (internal/live/check's TestIdentityGolden), so a change here is its own
// unit of work with its own evidence, not a side effect of the tag fix.
func configValue(block *configschema.Block, val cty.Value) cty.Value {
	if block == nil || val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return val
	}

	vals := make(map[string]cty.Value, len(block.Attributes)+len(block.BlockTypes))
	for name, attr := range block.Attributes {
		v := val.GetAttr(name)
		switch {
		case attr.Computed && !attr.Optional && !attr.Required:
			vals[name] = cty.NullVal(v.Type())
		case attr.NestedType != nil:
			vals[name] = configNestedObject(attr.NestedType, v)
		default:
			vals[name] = v
		}
	}
	for name, nested := range block.BlockTypes {
		v := val.GetAttr(name)
		switch nested.Nesting {
		case configschema.NestingSingle, configschema.NestingGroup:
			vals[name] = configValue(&nested.Block, v)
		default:
			vals[name] = mapElementsForConfig(v, func(elem cty.Value) cty.Value {
				return configValue(&nested.Block, elem)
			})
		}
	}
	return cty.ObjectVal(vals)
}

// configNestedObject is configValue for an attribute whose type is a nested
// object rather than a block.
func configNestedObject(obj *configschema.Object, val cty.Value) cty.Value {
	if val == cty.NilVal || val.IsNull() || !val.IsKnown() {
		return val
	}

	one := func(v cty.Value) cty.Value {
		if v.IsNull() || !v.IsKnown() {
			return v
		}
		vals := make(map[string]cty.Value, len(obj.Attributes))
		for name, attr := range obj.Attributes {
			av := v.GetAttr(name)
			switch {
			case attr.Computed && !attr.Optional && !attr.Required:
				vals[name] = cty.NullVal(av.Type())
			case attr.NestedType != nil:
				vals[name] = configNestedObject(attr.NestedType, av)
			default:
				vals[name] = av
			}
		}
		return cty.ObjectVal(vals)
	}

	if obj.Nesting == configschema.NestingSingle || obj.Nesting == configschema.NestingGroup {
		return one(val)
	}
	return mapElementsForConfig(val, one)
}

// mapElementsForConfig rebuilds a collection with every element passed
// through f, preserving the collection kind. An empty or unknown collection
// comes back untouched, since there is nothing to map and rebuilding one
// risks changing its type - and so does a marked one: this helper only ever
// feeds [objchange.ProposedNew] a config value, never an identity component
// or a log line, but LengthInt and ElementIterator both panic on a marked
// receiver, so the same "leave it alone" answer covers both reasons.
func mapElementsForConfig(val cty.Value, f func(cty.Value) cty.Value) cty.Value {
	if val == cty.NilVal || val.IsNull() || !val.IsKnown() || val.IsMarked() || val.LengthInt() == 0 {
		return val
	}

	ty := val.Type()
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		elems := make(map[string]cty.Value)
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			elems[k.AsString()] = f(v)
		}
		if ty.IsObjectType() {
			return cty.ObjectVal(elems)
		}
		return cty.MapVal(elems)
	default:
		var elems []cty.Value
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			elems = append(elems, f(v))
		}
		if ty.IsSetType() {
			return cty.SetVal(elems)
		}
		return cty.ListVal(elems)
	}
}

// withReplacedAttrs is obj with the named top-level attributes replaced by
// updates, and everything else carried across unchanged.
func withReplacedAttrs(obj cty.Value, updates map[string]cty.Value) cty.Value {
	ty := obj.Type()
	vals := make(map[string]cty.Value, len(ty.AttributeTypes()))
	for name := range ty.AttributeTypes() {
		if v, ok := updates[name]; ok {
			vals[name] = v
			continue
		}
		vals[name] = obj.GetAttr(name)
	}
	return cty.ObjectVal(vals)
}

// orderWork splits the resolutions into the work lists Build runs, in the
// order it runs them: every concrete instance first, in address order, then
// the parent-derived instances in dependency order.
//
// needsDiscovery is passed straight through to the omission list. cyclic
// holds parent-derived instances that could not be ordered because they
// form a dependency cycle among themselves. Ordering only ever needs to
// consider edges to other parent-derived instances: an edge to a concrete
// parent is satisfied by the time the derived phase starts, and an edge to
// a needs-discovery parent is never satisfiable and is handled as a missing
// parent at render time rather than as an ordering constraint.
//
// recordBacked holds GitHub issue #73's record-backed instances
// (identity.ClassRecordBacked), materialized from the record store rather
// than from any of the other four lists' identity machinery - see
// builder.materializeRecord.
//
// located holds GitHub issue #270's record-located instances
// (identity.ClassRecordLocated). It is a list of its own and NOT part of
// needsDiscovery, which is the whole reason this function has an explicit
// case for the class rather than leaving it to the default below: marker
// discovery is a tag sweep, a located type has no tag by definition, and
// routing one there would make the run lint clean and then fail at apply
// against internal/live/stamp's unmarked-apply refusal - a plan refusal
// traded for an apply refusal, which is forbidden. See
// TestOrderWorkRoutesLocatedExplicitly.
func orderWork(resolutions []identity.Resolution) (concrete, derived, needsDiscovery, cyclic, recordBacked, located []identity.Resolution) {
	sorted := make([]identity.Resolution, len(resolutions))
	copy(sorted, resolutions)
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Addr.String() < sorted[j].Addr.String()
	})

	var pending []identity.Resolution
	for _, r := range sorted {
		switch r.Class {
		case identity.ClassConcrete:
			concrete = append(concrete, r)
		case identity.ClassParentDerived:
			pending = append(pending, r)
		case identity.ClassRecordBacked:
			recordBacked = append(recordBacked, r)
		case identity.ClassRecordLocated:
			located = append(located, r)
		default:
			needsDiscovery = append(needsDiscovery, r)
		}
	}

	inPending := make(map[string]bool, len(pending))
	for _, r := range pending {
		inPending[r.Addr.String()] = true
	}

	done := make(map[string]bool, len(pending))
	for len(pending) > 0 {
		var stuck []identity.Resolution
		progressed := false

		for _, r := range pending {
			ready := true
			if r.Formula != nil {
				for _, p := range r.Formula.Parents {
					key := p.String()
					if inPending[key] && !done[key] {
						ready = false
						break
					}
				}
			}
			if !ready {
				stuck = append(stuck, r)
				continue
			}
			derived = append(derived, r)
			done[r.Addr.String()] = true
			progressed = true
		}

		if !progressed {
			// Everything left depends on something else left: a cycle.
			cyclic = stuck
			break
		}
		pending = stuck
	}

	return concrete, derived, needsDiscovery, cyclic, recordBacked, located
}

// Schemas is how [EmptyImportIdentityDiagnostics] learns which identity
// attributes a resource type's provider actually has - the same subset of
// [github.com/intentius/choudoufu/internal/tofu.Schemas] that
// [github.com/intentius/choudoufu/internal/live/stamp.Schemas] declares for
// the same reason, so an offline caller that already acquired schemas for
// stamping can hand the same value here.
type Schemas interface {
	ResourceTypeConfig(provider addrs.Provider, resourceMode addrs.ResourceMode, resourceType string) (*providers.Schema, uint64)
}

// CyclicIdentityDiagnostics reports every "Cyclic parent-derived identities"
// refusal [Build] would raise, computed from resolutions alone: [orderWork]
// classifies a parent-derived resolution as cyclic purely from the
// Addr/Formula.Parents graph among resolutions, before any provider is asked
// to import or read anything. This is that half of [builder.run] (see its
// "for _, r := range cyclic" loop) with the live-materializing halves left
// out, so a caller with no provider handle can still see it.
//
// # It cannot fire over resolutions that came from identity.ResolveWith
//
// GitHub issue #262 asked whether any configuration makes this refusal
// reach a user through internal/live/check's Analyze, and the answer is no,
// by construction rather than for want of a fixture:
//
//   - Every [Part] carrying a non-nil ParentRef is built by identity's
//     parentPart, and parentPart returns nothing unless r.instance already
//     resolved that parent to completion. A cyclic configuration therefore
//     fails inside identity, as "Circular identity reference", and neither
//     instance reaches a Resolution at all.
//   - identity's classify is the only place a [Formula] is constructed, and
//     it reads Parents off those same Parts.
//
// So a resolution's Formula.Parents can only name instances that finished
// resolving before it did. That orders the graph by completion time, which
// is acyclic, and orderWork's cyclic list is consequently always empty for
// anything ResolveWith produced. The refusal is defence in depth against a
// bug in identity resolution - which is what its own detail text says - and
// not a refusal a configuration trips. Any caller assembling resolutions by
// some other route still needs it.
//
// internal/live/check's TestAnalyzeCannotReachCyclicParentDerivedIdentities
// pins the empirical half over the configuration that shape describes.
func CyclicIdentityDiagnostics(resolutions []identity.Resolution) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	_, _, _, cyclic, _, _ := orderWork(resolutions)
	for _, r := range cyclic {
		detail := fmt.Sprintf(
			"The identities of %s and the instances it derives from refer to each other in a cycle, so there is no order in which they can be read. This is a bug in identity resolution: a parent-derived identity must name parents that are resolvable first.",
			r.Addr,
		)
		diags = diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cyclic parent-derived identities", detail))
	}
	return diags
}

// EmptyImportIdentityDiagnostics reports every "Empty import identity"
// refusal [Build] would raise for a [identity.ClassConcrete] resolution,
// computed with no live call: [importTarget] decides purely from the
// resolution's own statically-resolved identity/values and the provider's
// schema whether an identity object or an import-ID string can be built, and
// [importAndRead] refuses before ever touching a provider when neither can.
//
// A [identity.ClassParentDerived] resolution is not checked here. Its
// identity is a formula over a parent's live value ([builder.renderFormula]
// reads b.live, populated only by a prior [builder.materialize] call against
// a real provider), so whether its import target ends up empty cannot be
// decided offline - the omission is real, not an oversight.
//
// cfg supplies each resolution's provider configuration, the same way
// [builder.providerFor] does when a resource block still declares the
// instance; a resolution whose block is gone (Undeclared) is skipped, since
// this entry point has no [Options.UndeclaredProvider] to fall back to and
// guessing one would be worse than staying silent about it.
func EmptyImportIdentityDiagnostics(cfg *configs.Config, resolutions []identity.Resolution, schemas Schemas) tfdiags.Diagnostics {
	var diags tfdiags.Diagnostics
	concrete, _, _, _, _, _ := orderWork(resolutions)
	for _, r := range concrete {
		if r.Undeclared {
			continue
		}
		modCfg, ok := identity.ConfigForModule(cfg, r.Addr.Module)
		if !ok || modCfg == nil || modCfg.Module == nil {
			continue
		}
		rc := modCfg.Module.ManagedResources[r.Addr.Resource.Resource.String()]
		if rc == nil {
			continue
		}
		providerAddr := providerConfigAddr(modCfg, rc)
		typeName := r.Type()
		schema, _ := schemas.ResourceTypeConfig(providerAddr.Provider, addrs.ManagedResourceMode, typeName)
		if schema == nil {
			continue
		}
		w := wanted{
			addr:       r.Addr,
			importID:   r.ImportID,
			identity:   r.Identity,
			values:     r.IdentityValues,
			undeclared: r.Undeclared,
		}
		target := importTarget(w, *schema)
		if !target.IsIdentityBased() && !target.IsIDBased() {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Empty import identity",
				fmt.Sprintf("Nothing was computed as the import identity for a %s: no identity object and no import ID. For a type identified by several attributes with no separator between them, the identity object is the only form there is (see internal/live/identity's IdentityObjectOnly), so an identity the provider's schema would not accept leaves nothing to import by - which is refused here rather than approximated with a string.", typeName),
			))
		}
	}
	return diags
}

// renderFormula turns a parent-derived resolution into a concrete import ID -
// and into the same identity attribute by attribute, when the formula carries
// that split - by reading its parents' live values out of the projection
// built so far. It records an omission and returns false when it cannot.
func (b *builder) renderFormula(r identity.Resolution) (string, map[string]string, bool) {
	if r.Formula == nil {
		detail := fmt.Sprintf("Identity resolution classified %s as parent-derived but attached no formula.", r.Addr)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Parent-derived identity with no formula", detail))
		b.omit(r.Addr, ReasonFailed, detail, "identity resolution gave it no formula to render.")
		return "", nil, false
	}

	// Check every parent first, so that the reason names the parent rather
	// than the attribute lookup that happened to fail first.
	for _, p := range r.Formula.Parents {
		if _, ok := b.live[p.String()]; ok {
			continue
		}
		b.omit(r.Addr, ReasonParentUnavailable,
			fmt.Sprintf(
				"%s is identified by a composite of its parents' live IDs, and %s is not in the projection: %s Without that parent's live ID there is no import identity for %s, so the plan will propose creating it.",
				r.Addr, p, b.causeFor(p), r.Addr,
			),
			fmt.Sprintf("its own parent %s is not in the projection.", p),
		)
		return "", nil, false
	}

	var lookupDiags tfdiags.Diagnostics
	lookup := func(parent addrs.AbsResourceInstance, attr string) (string, bool) {
		val, ok := b.live[parent.String()]
		if !ok {
			return "", false
		}
		s, err := attrString(val, attr)
		if err != nil {
			lookupDiags = lookupDiags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Cannot read a parent's identity from the projection",
				fmt.Sprintf(
					"The identity of %s is composed from %s.%s, but that value cannot be used: %s. The provider's object for the parent does not carry the identity attribute this resource type's import syntax needs.",
					r.Addr, parent, attr, err,
				),
			))
			return "", false
		}
		return s, true
	}

	id, ok := r.Formula.Render(lookup)
	var values map[string]string
	if ok {
		// The same lookups again over the same parts; both renders succeed
		// or neither does, since the attribute parts are a subset of the
		// whole.
		values, ok = r.Formula.RenderAttrs(lookup)
	}
	if !ok {
		b.diags = b.diags.Append(lookupDiags)
		detail := fmt.Sprintf("The identity formula for %s could not be rendered from its parents' live values.", r.Addr)
		if len(lookupDiags) > 0 {
			detail = lookupDiags[0].Description().Detail
		}
		b.omit(r.Addr, ReasonFailed, detail, "its identity formula could not be rendered from its parents' live values.")
		return "", nil, false
	}
	return id, values, true
}

// causeFor renders why a parent instance is not in the projection, in a
// form that reads as a clause inside its child's explanation. The chain
// has to stay legible: a route is missing because its route table needs
// discovery, not merely because it is missing.
func (b *builder) causeFor(parent addrs.AbsResourceInstance) string {
	if cause, ok := b.causes[parent.String()]; ok {
		return cause
	}
	return "it was not resolved at all, so nothing is known about it."
}

// materialize drives one instance's import: ImportResourceState with the
// given ID, then ReadResource to refresh what came back, then a write into
// the projection.
//
// undeclared says the instance has no resource block, which is the shape of
// a resource this estate owns and the configuration has stopped declaring.
// It is not an error and not a special kind of state entry: the object is
// read and written exactly as any other, and what makes the plan destroy it
// is the ordinary rule that a prior-state instance with no configuration is
// an orphan. The two things it cannot have are a provider read off its
// resource block and a dependency set read off its arguments; see
// [Options.UndeclaredProvider] for the first, and [builder.dependencies] for
// why the second is empty rather than guessed.
// The return is true whenever this call has said its last word on addr: the
// instance materialized, or was terminally omitted (no configuration for a
// declared reference, no provider, a provider or schema failure, absence, a
// failed read, encoding failure, or an ownership refusal). It is false in
// exactly one case: [builder.checkOwnership] found w.recordFirst's binding
// stale. [builder.applyRecordFirst] is the only caller that reads it; every
// other call site materializes unconditionally and has nothing further to
// decide from the result.

// providerUnavailableSeverity is every "could not use this provider" site
// in this file's one downgrade rule: [tfdiags.Warning] for a
// [ProviderConfigNotEvaluable] failure, [tfdiags.Error] for anything else
// (a genuinely broken plugin, missing credentials, an unreadable schema).
//
// It is unconditional on which instance is asking, unlike internal/
// command's statelessDiscoverProviderUnavailable, which also gates on
// whether some declared instance's identity depends on the failing
// provider (needsSet). That gate is not needed again here: every call
// site below only ever reaches [ProviderConfigNotEvaluable] for a
// provider whose discovery/sweep pass (if the estate had one) already
// downgraded the identical failure for the identical reason - a provider
// that DID configure successfully for discovery is cached
// (statelessProviders.ConfiguredProvider) and would not fail again here.
// So an instance reaching this function already had its identity settled
// some other way (a client-derived importID, or a real binding through a
// DIFFERENT, working provider); this read is only ever "does it already
// exist", and [builder.omitFailed] already treats "cannot tell" as
// "proceed as if it does not" - the same default a genuinely first-ever
// create gets. See [ProviderConfigNotEvaluable]'s own doc comment for the
// full safety argument.
func providerUnavailableSeverity(err error) tfdiags.Severity {
	var notEvaluable *ProviderConfigNotEvaluable
	if errors.As(err, &notEvaluable) {
		return tfdiags.Warning
	}
	return tfdiags.Error
}

// prepareRead is everything [builder.materialize] settles before it reads:
// which resource block and provider configuration the instance belongs to,
// the schema to read it against, the import target, and the prior-state seed
// to read it with. It is the whole of materialize's former head, lifted out
// unchanged for GitHub issue #585 so that [builder.readPrefetch] can settle
// it once, in loop order, on this goroutine, and then hand the resulting
// provider call to a worker.
//
// The one change of shape is that a head that decides the instance cannot be
// read returns that decision as a [readTerminal] rather than appending its
// own diagnostics and omission on the spot. Nothing else may: a prepared read
// is computed BEFORE the instances ahead of it in the loop have finished
// materializing, so anything this function wrote into the builder would land
// out of order. What it reads is deliberately confined to state no
// materialize tail ever writes - the configuration, [Options], the provider
// cache (idempotent and sticky, so asking earlier gives the same answer), and
// the record store, which nothing writes until write-back long after this
// pass. [builder.materialize] applies the terminal, and everything else it
// appends, at its own point in the sequence exactly as it always did.
func (b *builder) prepareRead(ctx context.Context, w wanted) readPrep {
	addr := w.addr
	typeName := addr.Resource.Resource.Type

	modPath := addr.Module.Module()
	var rc *configs.Resource
	var modEval *configs.StaticEvaluator
	if modCfg, ok := identity.ConfigForModule(b.cfg, addr.Module); ok && modCfg.Module != nil {
		rc = modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
		modEval = modCfg.Module.StaticEvaluator
	}
	if rc == nil && !w.undeclared {
		detail := fmt.Sprintf(
			"Identity resolution produced %s, but that resource block is not in the configuration the projection was given. The configuration and the resolutions do not match.",
			addr,
		)
		return readPrep{terminal: &readTerminal{
			diags:  tfdiags.Diagnostics(nil).Append(tfdiags.Sourceless(tfdiags.Error, "Resolved instance missing from the configuration", detail)),
			reason: ReasonFailed,
			detail: detail,
			cause:  omitFailedCause,
		}}
	}

	providerAddr, providerOK := b.providerFor(rc, modPath, typeName, addr)
	if !providerOK {
		detail := fmt.Sprintf(
			"%s is a resource this estate owns whose resource block is no longer in the configuration, and nothing in the configuration says which provider to read a %s through: it declares no provider that could serve the type and the run supplied none. The resource is left alone rather than read.",
			addr, typeName,
		)
		return readPrep{terminal: &readTerminal{
			diags:  tfdiags.Diagnostics(nil).Append(tfdiags.Sourceless(tfdiags.Warning, "No provider for an undeclared resource", detail)),
			reason: ReasonFailed,
			detail: detail,
			cause:  "no provider could be found to read it through.",
		}}
	}
	entry, err := b.providers.get(ctx, providerAddr)
	if err != nil {
		detail := err.Error()
		return readPrep{terminal: &readTerminal{
			diags: tfdiags.Diagnostics(nil).Append(tfdiags.Sourceless(providerUnavailableSeverity(err), "Provider unavailable", fmt.Sprintf(
				"Building the projection entry for %s needs provider %s, which could not be used: %s.", addr, providerAddr, detail,
			))),
			reason: ReasonFailed,
			detail: detail,
			cause:  omitFailedCause,
		}}
	}

	schema, schemaDiags := entry.resourceSchema(providerAddr, typeName)
	if schemaDiags.HasErrors() {
		return readPrep{terminal: &readTerminal{
			diags:  schemaDiags,
			reason: ReasonFailed,
			detail: schemaDiags[0].Description().Detail,
			cause:  omitFailedCause,
		}}
	}

	// GitHub issue #287 item 8 (and #395, #376): seed BEFORE the read, not
	// after, because the gap this closes is in what
	// [providers.Configured.ImportResourceState] hands the provider going
	// into [providers.Configured.ReadResource], not in anything this
	// package does with the result. See [configuredAttrsSeed]'s doc
	// comment for the mechanism, and [configuredTagsSeed]'s for why tags
	// keeps its own, separately gated seed rather than folding into the
	// general one.
	//
	// seedEval augments modEval with [Options.DataResults] when this run
	// has any - see [Options.DataResults]'s own doc comment for why:
	// aws_launch_configuration.user_data_base64 reads a data source, and
	// the bare module evaluator cannot resolve one. A nil DataResults (any
	// caller before that field existed) makes [identity.DataLookupFor]
	// return a nil lookup, and seedEval stays byte-identical to modEval.
	seedEval := modEval
	if lookup, _ := identity.DataLookupFor(b.opts.DataResults, modPath); lookup != nil && seedEval != nil {
		seedEval = seedEval.WithDataResults(lookup)
	}
	tagsSeed, tagsSeedOK := configuredTagsSeed(ctx, seedEval, modPath, rc, schema)
	attrsSeed, attrsSeedMarks := configuredAttrsSeed(ctx, seedEval, modPath, rc, schema, entry.schema.DataSources)
	if tagsSeedOK {
		if attrsSeed == nil {
			attrsSeed = make(map[string]cty.Value, 1)
		}
		attrsSeed["tags"] = tagsSeed
	}

	// [builder.residueSeedFor] fills in whatever [configuredAttrsSeed] and
	// [configuredTagsSeed] could not statically evaluate - a managed-
	// resource reference (#395's own shape) or a data-source reference
	// (aws_launch_configuration.user_data_base64's) - from this estate's
	// residue record, when one exists, the name is one only configuration
	// could ever have set ([residueConfigSourced]), and the record's own
	// captured identity does not disagree with w's (issue #398). This loop
	// used to appear twice in a row, back to back, over the identical
	// call - a merge artifact from #395 and #376 landing the same seed
	// independently - which cost one redundant record-store read per
	// instance and would have doubled issue #398's own extra read; removed
	// rather than kept as insurance, since the second copy could only ever
	// re-add names the first loop had already claimed.
	// Configuration wins whenever both name the same attribute: it is read
	// fresh on every run, where a residue record can be stale, so a name
	// already in attrsSeed is left exactly as configuration produced it.
	for name, val := range b.residueSeedFor(ctx, w, schema) {
		if _, ok := attrsSeed[name]; ok {
			continue
		}
		if attrsSeed == nil {
			attrsSeed = make(map[string]cty.Value)
		}
		attrsSeed[name] = val
	}

	return readPrep{
		rc:             rc,
		modPath:        modPath,
		providerAddr:   providerAddr,
		entry:          entry,
		schema:         schema,
		target:         importTarget(w, schema),
		attrsSeed:      attrsSeed,
		attrsSeedMarks: attrsSeedMarks,
	}
}

func (b *builder) materialize(ctx context.Context, w wanted) bool {
	addr := w.addr
	importID := w.importID
	typeName := addr.Resource.Resource.Type

	// GitHub issue #585: the plan for this read, and the answer to it, come
	// from [builder.readPrefetch] when the concrete phase started one for
	// this instance, and are computed here and now when it did not. Either
	// way the plan was built by [builder.prepareRead] from the same inputs
	// and the call was made by [importAndRead] with the same arguments; all
	// that moves is which goroutine waited for the network.
	f := b.readFor(ctx, w)
	if t := f.prep.terminal; t != nil {
		b.diags = b.diags.Append(t.diags)
		b.omit(addr, t.reason, t.detail, t.cause)
		return true
	}
	rc := f.prep.rc
	modPath := f.prep.modPath
	providerAddr := f.prep.providerAddr
	entry := f.prep.entry
	schema := f.prep.schema
	attrsSeed := f.prep.attrsSeed
	obj, importStub, status, matDiags := f.obj, f.importStub, f.status, f.diags

	if w.recordFirst && (status == statusAbsent || status == statusFailed) {
		// The record's binding did not pan out - the provider found
		// nothing at that identity, or erred trying to read it - and for a
		// recordFirst attempt that is not proof of anything: unlike
		// [builder.materializeLocated]'s genuinely markerless types, this
		// instance's identity.Class has an ordinary, proven path (a marker
		// sweep or static derivation) that never needed this record to
		// begin with. Reporting "absent" here as a final answer would
		// propose a CREATE the moment a stale record merely pointed at the
		// wrong id while the real, correctly-tagged object sits
		// unexamined - a duplicate, not a recovery. Reporting "failed" as
		// a hard error would abort the whole plan over an identity this
		// run no longer trusts, when the classic path might resolve the
		// same instance cleanly. Either way the fix is the same one
		// ownershipStale already uses: fall back to whatever
		// [builder.applyRecordFirst]'s caller would have done with no
		// record in play, and say nothing terminal about it here. matDiags
		// is deliberately dropped rather than appended - it describes what
		// this abandoned identity did, not a fact about the estate - so a
		// caller reading [tfdiags.Diagnostics.HasErrors] does not see this
		// run as failed over an attempt that is about to be retried.
		reason := "no live object"
		if status == statusFailed {
			reason = "an error"
			if len(matDiags) > 0 {
				reason = matDiags[0].Description().Summary
			}
		}
		log.Printf("[TRACE] projection: %s's record-first identity %q for %s came back %q; falling back to identity.Class's own path",
			addr, importID, typeName, reason)
		return false
	}
	b.diags = b.diags.Append(matDiags)

	switch status {
	case statusAbsent:
		// GitHub issue #596, before the omission below is allowed to stand:
		// "no such object" and "this resource has not been created yet" are
		// the same provider answer to two different questions, and the
		// second is an inference. Where this run has POSITIVELY identified
		// a live object as this instance's - the provider's own list call
		// returned it, carrying this estate's marker - the inference is
		// contradicted by evidence already in hand, and proposing a create
		// would duplicate live infrastructure. See
		// [builder.refuseListedButAbsent], which also states why a tagging-
		// API sighting deliberately does NOT reach it.
		if b.refuseListedButAbsent(addr, typeName, importID, w, rc != nil && !w.undeclared) {
			return true
		}
		b.omit(addr, ReasonAbsent,
			fmt.Sprintf(
				"The provider reports no %s exists with identity %q, so this resource has not been created yet. The plan will propose creating it.",
				typeName, importID,
			),
			fmt.Sprintf("the provider reports no %s exists with identity %q.", typeName, importID),
		)
		return true
	case statusFailed:
		detail := fmt.Sprintf("Reading %s with identity %q failed.", typeName, importID)
		if len(matDiags) > 0 {
			detail = fmt.Sprintf("Reading %s with identity %q failed: %s.", typeName, importID, matDiags[0].Description().Summary)
		}
		b.omitFailed(addr, detail)
		return true
	}

	// GitHub issue #402: scrub before anything downstream (ownership,
	// residue fill, the plan itself) ever sees the raw read. See
	// [scrubAmbientEcho]'s own doc comment for the defect this closes -
	// hashicorp/aws's S3 bucket sub-resource types echo the run's own
	// ambient AWS account id into a deprecated, non-Computed,
	// expected_bucket_owner-shaped argument on every read, unconditionally,
	// even when attrsSeed (computed above from this instance's own static
	// configuration) proves configuration set nothing for it - turning a
	// value nothing ever asked for into a forced replacement the moment
	// configuration omits the argument. Ahead of normalizeIdentityAttrs and
	// checkOwnership on purpose: neither reads this population of
	// attributes, and obj.Value is what every trace line and every
	// downstream comparison from here on reports.
	obj.Value = scrubAmbientEcho(schema, obj.Value, b.ambientContext(providerAddr, schema, obj.Identity), attrsSeed)

	// Adopt the provider's own spelling of an identity-bearing attribute
	// before anything downstream compares it to configuration - see
	// [builder.normalizeIdentityAttrs] (GitHub issue #281). Ahead of the
	// ownership check on purpose: a mismatch here is about what the value
	// SAYS, not who owns the object, and normalizing first means ownership
	// reads the same object every other check downstream will.
	//
	// importID is rewritten to match: it is built from the same raw
	// component values that just proved not to survive the provider's own
	// create-time plan, and every log line below this point - most of all
	// materialize's own "materialized ... from import identity" trace -
	// exists so an operator can compare what ran against what the live
	// system holds. Left unrewritten, that line would keep printing the
	// spelling this run just found unstable, on an instance whose state
	// no longer carries it.
	for name, newVal := range b.normalizeIdentityAttrs(ctx, entry.provider, schema, typeName, w, obj) {
		if oldVal, ok := w.values[name]; ok && oldVal != "" {
			importID = strings.Replace(importID, oldVal, newVal, 1)
		}
	}

	// Ownership is checked here, on the object the provider returned, and
	// before anything is written into the projection. Everything below this
	// point is what "the estate owns this" means in practice - a prior-state
	// entry the plan may update, and an orphan the plan may destroy once its
	// block is gone - so this is the one place the check belongs.
	// declared is deliberately not just "rc != nil": a surplus count member
	// or a sweep orphan can sit inside a resource block that still exists
	// (rc found by block address, not by this specific instance), and
	// w.undeclared is the resolution's own word on whether this exact
	// instance is one the configuration currently expands to. A surplus
	// member is the one case this still approximates - discovery's bind()
	// does not set Undeclared for it - and that is the same block-level
	// coarsening internal/live/stamp's PolicyUntag already documents,
	// rather than a new one.
	switch b.checkOwnership(addr, typeName, importID, schema, obj.Value, rc != nil && !w.undeclared, w.located, w.recordFirst) {
	case ownershipStale:
		// The record's binding did not survive being checked against the
		// live object's own marker; checkOwnership has already logged the
		// warning and nothing was written into the projection. The caller
		// ([builder.applyRecordFirst]) routes addr back through whatever
		// path its identity.Class would have taken with no record at all.
		return false
	case ownershipUnowned:
		return true
	}

	// GitHub issue #275's residue, applied AFTER the ownership check and
	// never before it. Ownership is decided on what the CLOUD said, and a
	// stored value must never be in a position to answer "does this estate
	// own this" - that is what the marker is for. Filling first would let a
	// record about a filename argue about a tag.
	b.fillResidueFor(ctx, addr, schema, obj, importStub)

	// GitHub issue #353's one bit, applied after the ownership check for
	// fillResidueFor's exact reason and read only for an instance whose
	// block still declares a create-time provisioner. If the last apply
	// left one failed, this is what makes the object arrive in the plan
	// graph as states.ObjectTainted - and a tainted prior object is what
	// stock turns into a synthetic Replace, which re-runs the provisioner
	// on the new object with no new execution machinery of any kind.
	if !b.applyProvisionedTaint(ctx, addr, rc, obj) {
		return true
	}

	if rc != nil {
		obj.Dependencies = b.dependencies(rc, modPath, schema)
	} else if len(w.dependsOn) > 0 {
		// [wanted.dependsOn]'s own doc comment: the ordering hint a
		// parent-scoped removal leg supplied in place of the configuration
		// reference this function has nothing to read for an undeclared
		// instance. Deduplicated and sorted only incidentally by going
		// through a ConfigResource set - a leg only ever supplies at most
		// one entry today, but nothing here assumes that stays true.
		seen := make(map[string]addrs.ConfigResource, len(w.dependsOn))
		for _, dep := range w.dependsOn {
			cr := dep.ConfigResource()
			seen[cr.String()] = cr
		}
		deps := make([]addrs.ConfigResource, 0, len(seen))
		for _, cr := range seen {
			deps = append(deps, cr)
		}
		sort.Slice(deps, func(i, j int) bool { return deps[i].String() < deps[j].String() })
		obj.Dependencies = deps
	}

	// obj.Value already carries the schema's own sensitivity here, applied by
	// [importAndRead] to the provider's wire answer - which is GitHub issue
	// #343's whole subject, and the reason that issue closed as a misreading
	// of this function rather than as a fix. Encode turns the marks into
	// AttrSensitivePaths, which under SkipRefresh is the whole of what the
	// plan's "before" side gets.
	//
	// Nothing between there and here removes a mark, and since GitHub issue
	// #365 slice 3 that is a property of [builder.fillResidueFor] rather than
	// of what it declines to touch. It used to hold because that function's
	// candidate filter refused a Sensitive attribute outright; under
	// `strict { secrets = "store" }` it fills one, from a record that holds
	// the value unmarked, and re-marks the result from this same schema with
	// [markSchemaSensitive] before returning. [residueMarkRecoverable] is
	// what makes that restoration exact rather than approximate.
	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), uint64(schema.IdentitySchemaVersion))
	if err != nil {
		detail := fmt.Sprintf("The object read for %s could not be encoded into the projection: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot encode a projected object", detail))
		b.omitFailed(addr, detail)
		return true
	}

	b.state.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, providerAddr, addrs.NoKey)
	b.live[addr.String()] = obj.Value
	b.materialized = append(b.materialized, addr)
	// dedupID is what [builder.materializedIdentity]'s key is built from -
	// deliberately [traceImportID]'s own fallback, not the bare importID
	// parameter. A type with no single import-ID string (every Component
	// supplies part of a composite, [identity.TypeIdentity.IdentityObjectOnly]'s
	// shape - aws_route53_record among them) leaves importID "" for EVERY
	// route into this function except the ordinary [identity.Class]-driven
	// concrete/derived ones: [builder.materializeFromRecord] (the
	// applyRecordFirst intercept every non-record-backed/-located
	// resolution tries FIRST, GitHub issue #364 unit A2) hands materialize
	// the persisted record's rec.ImportID verbatim, which is empty for
	// exactly this shape - the record carries only rec.Components. Before
	// this fix, [builder.run]'s ReasonSuperseded check (GitHub issue #404)
	// silently never registered such an instance at all: the OLD line
	// below only ever set the map when the raw importID happened to be
	// non-empty, so a record-first-materialized declared instance of a
	// composite-only type left no entry for recordOrphanReadSweep's own
	// (correctly composed, non-empty) undeclared resolution to match
	// against, and a live object relocated by ordinary parent-derived
	// re-discovery - the exact shape day2_rename's own e2e header comment
	// documents needing no `moved` block for - planned a destroy of the
	// object its new address was about to keep managing (GitHub issue
	// #410). traceImportID already composes the identical canonical
	// string from obj.Value for the trace line a few lines down whenever
	// importID is empty and the type's Components are known, using the
	// SAME [identity.LookupType] table [composeImportIDFromComponents]
	// (internal/live/discovery/recordorphan_read.go) reads to build the
	// undeclared side's own r.ImportID - so the two sides compare equal
	// once both go through it. Reaches every admitted type whose entry
	// carries Components with no single IdentityAttrs string, not
	// aws_route53_record specifically.
	dedupID := traceImportID(typeName, importID, obj.Value)
	if dedupID != "" {
		b.materializedIdentity[typeName+"\x00"+dedupID] = true
	}
	log.Printf("[TRACE] projection: materialized %s from import identity %q", addr, dedupID)
	return true
}

// materializeDeposed is GitHub issue #361's crash-window recovery: reads
// one deposed object discovery's collision-breaking branch matched against
// this estate's record ([DeposedBinding]) and, on success, folds it into
// the constructed state as Instances[key].Deposed[db.DeposedKey] via
// [states.Module.SetResourceInstanceDeposed] - stock's own shape, from
// which stock's own completely unmodified node_resource_deposed.go graph
// machinery takes over: a second, independent ReadResource before the
// destroy it proposes is finalized (design comment, section 4).
//
// Deliberately narrower than [builder.materialize]: no ownership check (the
// claimant this binding came from was already live-read and marker-checked
// by discovery before the record was ever consulted - see #361's design
// comment, section 4, item 1), no residue fill and no provisioner-taint
// application, because a deposed object is never updated - only refreshed
// and destroyed - so neither concern has anywhere to apply.
//
// A failure here (no provider, no schema, a read error) is reported and
// the binding is simply not folded in: the deposed object stays recorded
// but unread, which is the same "left recorded but unread" outcome
// [RecordStore.GetDeposed]'s own callers already tolerate elsewhere, never
// a reason to fail the whole projection. A live "absent" answer - the
// object was already destroyed, by a previous run's own recovery or by a
// human working around the estate - folds in nothing either, on purpose:
// [diffDeposedForWrite] clears the stale record entry on this same run's
// own write-back.
func (b *builder) materializeDeposed(ctx context.Context, db DeposedBinding) {
	addr := db.Addr
	typeName := addr.Resource.Resource.Type

	providerAddr := db.Provider
	if providerAddr.Provider.Type == "" {
		// No provider was recorded for this deposed object specifically
		// (or it failed to parse) - fall back to whatever provider serves
		// the current instance's own resource block, the same rule
		// [builder.materialize]'s own providerFor call uses.
		modPath := addr.Module.Module()
		var rc *configs.Resource
		if modCfg, ok := identity.ConfigForModule(b.cfg, addr.Module); ok && modCfg.Module != nil {
			rc = modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
		}
		var ok bool
		providerAddr, ok = b.providerFor(rc, modPath, typeName, addr)
		if !ok {
			detail := fmt.Sprintf(
				"%s carries a recorded deposed object (%s) from an earlier interrupted replace, but nothing says which provider to read it through: its own record carries no provider and the current resource block declares none either. The deposed object is left recorded but unread.",
				addr, db.DeposedKey,
			)
			b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Warning, "No provider for a deposed object", detail))
			return
		}
	}

	entry, err := b.providers.get(ctx, providerAddr)
	if err != nil {
		b.diags = b.diags.Append(tfdiags.Sourceless(providerUnavailableSeverity(err), "Provider unavailable", fmt.Sprintf(
			"Reading the deposed object recorded for %s (%s) needs provider %s, which could not be used: %s.", addr, db.DeposedKey, providerAddr, err,
		)))
		return
	}
	schema, schemaDiags := entry.resourceSchema(providerAddr, typeName)
	if schemaDiags.HasErrors() {
		b.diags = b.diags.Append(schemaDiags)
		return
	}

	w := wanted{addr: addr, importID: db.ImportID, values: db.Components}
	obj, _, status, matDiags := importAndRead(ctx, entry.provider, schema, typeName, importTarget(w, schema), db.ImportID, db.Components, nil, nil)
	switch status {
	case statusAbsent:
		log.Printf("[TRACE] projection: %s's recorded deposed object %s (%s) no longer exists live; not folded into the projection", addr, db.DeposedKey, traceImportID(typeName, db.ImportID, cty.NilVal))
		return
	case statusFailed:
		detail := fmt.Sprintf("Reading the deposed object recorded for %s (%s) failed.", addr, db.DeposedKey)
		if len(matDiags) > 0 {
			detail = fmt.Sprintf("Reading the deposed object recorded for %s (%s) failed: %s.", addr, db.DeposedKey, matDiags[0].Description().Summary)
		}
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read a recorded deposed object", detail))
		return
	}
	b.diags = b.diags.Append(matDiags)

	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), uint64(schema.IdentitySchemaVersion))
	if err != nil {
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot encode a deposed object", fmt.Sprintf(
			"The deposed object recorded for %s (%s) could not be encoded into the projection: %s.", addr, db.DeposedKey, err,
		)))
		return
	}

	b.state.EnsureModule(addr.Module).SetResourceInstanceDeposed(addr.Resource, db.DeposedKey, src, providerAddr, addrs.NoKey)
	log.Printf("[TRACE] projection: folded in the recorded deposed object for %s (%s) from import identity %q", addr, db.DeposedKey, db.ImportID)
}

// traceImportID is what [builder.materialize]'s own trace line prints in
// importID's place. importID is empty whenever this instance imported
// through an identity OBJECT rather than a joined string - not only
// [identity.TypeIdentity.IdentityObjectOnly] (no separator exists to join
// with), but also, since GitHub issue #364 unit B's applyRecordFirst,
// exactly the ordinary case for a type whose provider serves a composite
// wire identity schema at all: [wanted.importID] is never populated from a
// record's Components map (see [builder.materializeFromRecord]), because
// [importTarget] does not need a string for such a type - it reads the
// object straight through the identity schema via [identityFromValues].
// The import itself is unaffected either way; this only restores the
// trace's own value as a human-checkable audit line (and the one thing a
// live-plan-invoking crossing script can grep to assert an identity BY
// VALUE - see live/e2e/corpus-mastino-dns/run.sh, which broke exactly this
// way the day the record-first path started reaching aws_route53_record).
// The same ratified Components chain [locatedRatifiedComponentsRecord]
// already uses to compose a record's own import ID is reused here, against
// the object this call just read rather than the one migrate recorded -
// reaching every type with a ratified composite identity, not only this
// one, and never naming a resource type.
func traceImportID(typeName, importID string, obj cty.Value) string {
	if importID != "" {
		return importID
	}
	ti, ok := identity.LookupType(typeName)
	if !ok || len(ti.Components) == 0 || obj == cty.NilVal || !obj.IsWhollyKnown() {
		return importID
	}
	composed, _, ok := identity.ComponentsFromValue(ti, traceNullifyEmptyOptionalStrings(obj))
	if !ok {
		return importID
	}
	return composed
}

// traceNullifyEmptyOptionalStrings is [traceImportID]'s own narrow
// normalization, scoped to this trace line alone and touching nothing
// [identity.ComponentsFromValue]'s other, plan-affecting callers read: a
// provider read (as opposed to a statically evaluated HCL value, where an
// omitted attribute genuinely evaluates to null) commonly answers an
// unset optional string attribute with "" rather than null - Route 53's
// own aws_route53_record.set_identifier does, over floci and observably in
// AWS's own SDKv2-shaped provider surface generally - and
// [identity.Component.OmitIfAbsent] only tests for null, so an empty
// string reads as "present" and the composed string picks up a bare
// trailing separator with nothing after it. Left for [identity.
// ComponentsFromValue]'s core callers to fix if they ever need it (that
// would touch the pinned identity golden set and is out of scope for a
// display-only trace line); here it only ever removes a component that
// would otherwise render as an empty segment, never adds or changes one
// that has real content.
func traceNullifyEmptyOptionalStrings(obj cty.Value) cty.Value {
	// A marked receiver is unsafe for GetAttr (internal/live/marksafe);
	// [identity.ComponentsFromValue] already refuses a marked val outright
	// (val.IsMarked() at its own top), so leaving it untouched here still
	// reaches that same refusal rather than a panic.
	if obj.IsMarked() || !obj.Type().IsObjectType() {
		return obj
	}
	attrTypes := obj.Type().AttributeTypes()
	vals := make(map[string]cty.Value, len(attrTypes))
	changed := false
	for name, at := range attrTypes {
		v := obj.GetAttr(name)
		if at == cty.String && !v.IsNull() && v.IsWhollyKnown() && !v.IsMarked() && v.AsString() == "" {
			v = cty.NullVal(cty.String)
			changed = true
		}
		vals[name] = v
	}
	if !changed {
		return obj
	}
	return cty.ObjectVal(vals)
}

// materializeRecord is [builder.materialize]'s counterpart for GitHub issue
// #73's record-backed instances (identity.ClassRecordBacked): it bypasses
// importAndRead entirely and hydrates prior state from
// [Options.RecordStore] instead of a provider's ImportResourceState/
// ReadResource conversation. There is no ownership check - a record-backed
// instance has no cloud object and therefore no ownership tag to carry, the
// same reason [identity.ClassRecordBacked]'s doc comment gives for why no
// marker sweep ever applies to it either - and no import identity of any
// kind: the record itself is the whole of what makes the instance exist.
//
// undeclared marks an instance builder.discoverOrphanedRecords found in the
// store with no matching resource block - a record-backed resource whose
// configuration was removed. It is the record-backed analog of
// [wanted.undeclared]: since a record-backed instance carries no marker
// either, the record store's own key listing is the only way such a
// resource is ever found again, which is exactly what
// discoverOrphanedRecords is for. An undeclared instance's provider is
// implied from its bare type name ([addrs.ImpliedProviderForUnqualifiedType])
// rather than read from a resource block that no longer exists, and it gets
// no computed dependency set, for the same reason [builder.materialize]
// gives undeclared marker-found instances: destroy ordering for a resource
// whose configuration is gone is exactly what a state file remembers and a
// projection cannot.
//
// A provider connection is still needed, for the same two reasons ordinary
// materialize needs one: [providers.Schema] is what lets the stored,
// self-describing value be converted onto today's schema before it is
// trusted (a record written under an older provider version might not
// conform), and configs.Resource plus that schema is what
// builder.dependencies needs to compute destroy ordering.
func (b *builder) materializeRecord(ctx context.Context, addr addrs.AbsResourceInstance, undeclared bool) {
	typeName := addr.Resource.Resource.Type

	modPath := addr.Module.Module()
	modCfg, modOK := identity.ConfigForModule(b.cfg, addr.Module)
	var rc *configs.Resource
	if modOK && modCfg.Module != nil {
		rc = modCfg.Module.ManagedResources[addr.Resource.Resource.String()]
	}
	if rc == nil && !undeclared {
		detail := fmt.Sprintf(
			"Identity resolution produced %s as record-backed, but that resource block is not in the configuration the projection was given. The configuration and the resolutions do not match.",
			addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Resolved instance missing from the configuration", detail))
		b.omitFailed(addr, detail)
		return
	}

	if b.opts.RecordStore == nil {
		// Reachable only if a caller resolves a RECORD_ADMITTED type without
		// also configuring a store - internal/live/lint's admission gate is
		// supposed to make that impossible, so this is an internal
		// inconsistency rather than a configuration mistake an operator
		// could have made.
		detail := fmt.Sprintf(
			"%s resolved to a record-backed identity, but no record store was configured for this projection. This is an internal inconsistency: a RECORD_ADMITTED type should never reach here without a live block's record_store.",
			addr,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Record-backed instance with no record store", detail))
		b.omitFailed(addr, detail)
		return
	}

	var providerAddr addrs.AbsProviderConfig
	if rc != nil {
		providerAddr = providerConfigAddr(modCfg, rc)
	} else {
		providerAddr = addrs.AbsProviderConfig{
			Module:   addrs.RootModule,
			Provider: addrs.ImpliedProviderForUnqualifiedType(impliedProviderName(typeName)),
		}
	}
	entry, err := b.providers.get(ctx, providerAddr)
	if err != nil {
		detail := err.Error()
		b.diags = b.diags.Append(tfdiags.Sourceless(providerUnavailableSeverity(err), "Provider unavailable", fmt.Sprintf(
			"Building the projection entry for %s needs provider %s, which could not be used: %s.", addr, providerAddr, detail,
		)))
		b.omitFailed(addr, detail)
		return
	}
	schema, schemaDiags := entry.resourceSchema(providerAddr, typeName)
	if schemaDiags.HasErrors() {
		b.diags = b.diags.Append(schemaDiags)
		b.omitFailed(addr, schemaDiags[0].Description().Detail)
		return
	}

	env, version, exists, err := b.opts.RecordStore.getRaw(ctx, addr)
	if err != nil {
		detail := fmt.Sprintf("Reading the persisted record for %s failed: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot read a persisted record", detail))
		b.omitFailed(addr, detail)
		return
	}
	if !exists {
		b.omit(addr, ReasonAbsent,
			fmt.Sprintf(
				"No persisted record exists yet for %s, so this resource has not been created yet. The plan will propose creating it.",
				addr,
			),
			"no persisted record exists yet for it.",
		)
		return
	}
	if env.Object == nil {
		detail := fmt.Sprintf("The persisted record for %s carries no object to materialize (kind %q).", addr, env.Kind)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot decode a persisted record", detail))
		b.omitFailed(addr, detail)
		return
	}
	val, private, status, err := decodeObjectValue(env.Object)
	if err != nil {
		detail := fmt.Sprintf("The persisted record for %s could not be read: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot decode a persisted record", detail))
		b.omitFailed(addr, detail)
		return
	}
	// The record's own sensitivity comes off before the conversion and goes
	// back on after it, which is the same order
	// states.ResourceInstanceObjectSrc.Decode uses: unmarshal, then
	// MarkWithPaths. Converting a marked value would work - cty's convert
	// carries marks - but the paths are needed separately anyway, and doing
	// it in one visible place keeps every accessor below this line reading
	// a value nothing has marked.
	unmarkedVal, sensitive := val.UnmarkDeepWithPaths()
	converted, err := convert.Convert(unmarkedVal, schema.Block.ImpliedType())
	if err != nil {
		detail := fmt.Sprintf(
			"The persisted record for %s does not fit %s's current schema: %s. This usually means the record was written under a different provider version. Delete the stale record from the store to let the plan re-create it, or pin the provider version that wrote it.",
			addr, typeName, err,
		)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Persisted record does not match the current schema", detail))
		b.omitFailed(addr, detail)
		return
	}
	// Re-marking before obj.Encode is the whole point of persisting the
	// paths: Encode derives AttrSensitivePaths from the value's own marks,
	// and that is what the plan graph's "before" side decodes back out.
	// Without this the plan proposes a sensitivity-only in-place update for
	// every sensitive attribute, every run, forever.
	if len(sensitive) > 0 {
		converted = converted.MarkWithPaths(sensitive)
	}
	// The schema's own sensitivity goes on top of the record's, which is what
	// GitHub issue #343 turned out to be about once the concrete-cloud half
	// was found already answered by importAndRead. The record remembers what
	// was sensitive when it was WRITTEN; the plan's "after" side is marked
	// from the schema as it is TODAY. A record written before this fork
	// persisted paths at all - or before the provider marked an attribute
	// sensitive - carries strictly fewer paths than today's schema implies,
	// and the difference is a perpetual sensitivity-only diff exactly like
	// the concrete-cloud one would be. Composing both sources is what
	// upstream's refresh does; the record is this path's priorPaths.
	converted = markSchemaSensitive(converted, schema.Block)

	// status came out of the record itself (decodeRecordPayload), not a
	// hardcoded states.ObjectReady: a tainted object has to stay tainted
	// through this hydration, or the ordinary tofu plan graph never sees
	// the reason to force a replace (issue #216 - a record-backed
	// resource has no state file to carry that bit anywhere else).
	obj := &states.ResourceInstanceObject{
		Status:  status,
		Value:   converted,
		Private: private,
	}
	if rc != nil {
		obj.Dependencies = b.dependencies(rc, modPath, schema)
	}

	src, err := obj.Encode(schema.Block.ImpliedType(), uint64(schema.Version), uint64(schema.IdentitySchemaVersion))
	if err != nil {
		detail := fmt.Sprintf("The persisted record for %s could not be encoded into the projection: %s.", addr, err)
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot encode a projected object", detail))
		b.omitFailed(addr, detail)
		return
	}

	b.state.EnsureModule(addr.Module).SetResourceInstanceCurrent(addr.Resource, src, providerAddr, addrs.NoKey)
	b.live[addr.String()] = converted
	b.materialized = append(b.materialized, addr)
	b.recordVersions = append(b.recordVersions, RecordVersion{Addr: addr, Version: version})
	log.Printf("[TRACE] projection: materialized %s from a persisted record", addr)
}

// discoverOrphanedRecords is record-backed resources' answer to the
// question ordinary marker discovery answers for cloud resources: "what
// does this estate own that its current configuration no longer declares?"
// A record-backed instance carries no marker and has no cloud object a
// sweep could find, so [Options.RecordStore]'s own key listing is the only
// remaining source of truth - every key under [Options.RecordKeyPrefix] IS
// the estate's set of record-backed resources, the same way an estate's
// tagged live objects are its set of cloud-backed ones.
//
// known is the set of addresses already materialized from a resolved
// resource block (builder.run's declaredRecordBacked); every other decoded
// key is undeclared, and is materialized the same way an undeclared
// marker-found resource is: written into the projection so the ordinary
// no-configuration-for-this-prior-state-entry rule makes the plan propose
// destroying it, and its record deleted by WriteBack once the destroy
// succeeds and the address drops out of the final state.
//
// A key this package cannot make sense of - [RecordKey]'s reverse,
// [RecordAddr], returning false - is skipped rather than treated as an
// error: a record store's namespace is not a promise that every key in it
// is one of this package's, and a stray or foreign key here is not this
// run's business to fail over.
func (b *builder) discoverOrphanedRecords(ctx context.Context, known map[string]bool) {
	if b.opts.RecordStore == nil {
		return
	}
	keys, err := b.opts.RecordStore.List(ctx)
	if err != nil {
		b.diags = b.diags.Append(tfdiags.Sourceless(tfdiags.Error, "Cannot list the record store",
			fmt.Sprintf("Listing the record store to find record-backed resources whose configuration block was removed failed: %s.", err),
		))
		return
	}
	for _, key := range keys {
		addr, ok := RecordAddr(b.opts.RecordStore.Prefix(), key)
		if !ok {
			continue
		}
		if known[addr.String()] {
			continue
		}
		// GitHub issue #364/#270: a kind=identity key is never delete
		// authority. Before this envelope collapse, that was true by
		// construction - the located/residue/provisioned namespaces were
		// simply never enumerated. Now every per-instance fact shares one
		// enumerable root, so the kind check is what keeps a lost or stale
		// identity, residue or provisioner-taint key from being proposed
		// for destruction the way an undeclared record-backed key is.
		kind, kindExists, kindErr := b.opts.RecordStore.peekKind(ctx, key)
		if kindErr != nil {
			// A key this run cannot even read the kind of is treated the
			// same as one it cannot decode at all - skipped, not failed:
			// [RecordAddr]'s own doc comment already accepts that a
			// record store's namespace is not a promise every key in it is
			// this package's, and materializeRecord's own read a moment
			// from now would hit the identical error and report it loudly
			// if this address turns out to matter.
			continue
		}
		if !kindExists || kind != recordKindObject {
			continue
		}
		b.materializeRecord(ctx, addr, true)
	}
}

type materializeStatus int

const (
	statusMaterialized materializeStatus = iota
	// statusAbsent means the provider answered normally that there is no
	// such object. Not an error.
	statusAbsent
	// statusFailed means the provider could not answer. Always accompanied
	// by error diagnostics.
	statusFailed
)

// notFoundDiagnosticSignals are the textual shapes an error-severity
// diagnostic out of ImportResourceState takes when a provider is actually
// answering "there is no such object", not failing to answer at all.
// Textual is all there can be: a diagnostic that has crossed the provider
// plugin protocol carries only Summary/Detail strings (tfdiags.Description),
// nothing structured about the underlying provider error survives the wire.
//
//   - "couldn't find resource" is the exact, hardcoded default message
//     terraform-plugin-sdk's retry.NotFoundError renders when a provider's
//     internal finder comes back empty and sets no more specific text. It
//     is a generic SDK convention used across the whole of
//     terraform-provider-aws wherever a resource's Read is built on a
//     "find the live object or report NotFoundError" finder, not a
//     type-specific string. aws_lambda_permission - whose import lookup
//     calls GetPolicy on the function, not the permission, and so 404s
//     with this shape the moment the function itself does not exist yet
//     either - is a confirmed instance (issue #297), not the only one this
//     is meant to cover.
//   - "ResourceNotFoundException" is AWS's own API error code, for a
//     provider that surfaces the untranslated API error instead of going
//     through the generic finder convention above.
var notFoundDiagnosticSignals = []string{
	"couldn't find resource",
	"ResourceNotFoundException",
}

// notFoundDiagnostics reports whether every error-severity diagnostic in
// diags matches one of [notFoundDiagnosticSignals], so an
// ImportResourceState response that came back with diagnostics can still be
// folded into statusAbsent the same as an empty ImportedResources list or a
// null read result - the "ordinary absence" this package's doc comments
// promise. detail is the first matching diagnostic's rendered text, for the
// warning that replaces the discarded error.
//
// A response mixing a not-found-shaped diagnostic with any other
// error-severity diagnostic - a credentials problem, a malformed request, a
// genuine provider failure - does not match: every error present has to be
// not-found-shaped, or this reports false and the caller's existing hard
// stop applies untouched. That also means a warning-only response (no
// errors at all) never reaches this function, since it is only consulted
// under importResp.Diagnostics.HasErrors().
func notFoundDiagnostics(diags tfdiags.Diagnostics) (bool, string) {
	sawError := false
	detail := ""
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		sawError = true
		desc := d.Description()
		text := strings.TrimSpace(desc.Summary + ": " + desc.Detail)
		matched := false
		for _, signal := range notFoundDiagnosticSignals {
			if strings.Contains(desc.Summary, signal) || strings.Contains(desc.Detail, signal) {
				matched = true
				break
			}
		}
		if !matched {
			return false, ""
		}
		if detail == "" {
			detail = text
		}
	}
	return sawError, detail
}

// noImporterDiagnosticSignals, noImporterDiagnostics and
// synthesizeNoImporterStub used to live here. GitHub issue #388's plan-node
// seam (internal/tofu/node_resource_plan_instance.go's importState) needed
// the identical classification and synthesis for the same provider
// response, reached through a different path that this package's own
// import of internal/tofu (elsewhere in this package, for unrelated
// reasons) rules out sharing directly - internal/tofu must never import
// this package back. Both now call internal/live/noimporter, a leaf
// package with no dependency on either side; see its own doc comment for
// why. This package's own behavior, wording and every test below are
// unchanged - only the two functions' bodies moved.

// configuredAttrsSeed generalizes [configuredTagsSeed]'s mechanism (GitHub
// issue #287 item 8) from "tags" specifically to every OTHER flat,
// non-identity, non-Computed attribute the resource's own configuration
// statically sets: whatever the configuration declares there is exactly
// what a genuinely persisted state file would already hold, and seeding
// the import stub with it before ReadResource closes the same class of
// ambiguity "tags" closes, for any provider whose Read branches on "was
// this argument already present in prior state" rather than reading it
// from the remote at all.
//
// # Two units, reconciled onto one implementation
//
// corpus-eks-basic/test_plan and corpus-ecs-fargate/test_plan each hit a
// member of this same class independently (aws_launch_configuration.
// user_data_base64 and aws_ecs_service.task_definition /
// aws_ecs_task_definition.track_latest+skip_destroy, respectively) and
// each generalized configuredTagsSeed's mechanism on its own branch.
// Reconciled 2026-08-24 (rebased corpus-ecs-fargate's branch onto main
// after corpus-eks-basic's landed first): both kept "tags" on its own,
// separately gated [configuredTagsSeed] rather than folding it in here -
// seeding it twice from two independent paths is exactly the drift a
// single mechanism is supposed to prevent, and eks-basic's own test
// (TestConfiguredAttrsSeedSeedsStaticNonTagAttributes) asserts this
// function's own map never carries "tags". Where the two branches'
// PROTOCOL TEST differed - eks-basic's excluded only WriteOnly, NestedType
// and identity attributes, with no Computed check at all, against
// ecs-fargate's own "Required, or Optional and never Computed" - the
// reconciliation took the STRICTER of the two (this function's own
// Computed check, below), the safer intersection: every attribute either
// branch's own tests actually assert gets seeded (user_data_base64, name,
// task_definition, track_latest, skip_destroy) is Required or
// Optional-and-not-Computed, so both branches' test suites pass unmodified
// under the narrower rule, and an Optional+Computed attribute - one the
// PROVIDER, not only configuration, may still answer for - is never
// seeded by either.
//
// # The original case, generalized
//
// Issue #287 item 8: a stamped resource with a provider-level default_tags
// block showed a permanent, spurious "tags" in-place diff, re-adding the
// same keys on every single plan. Verified directly against a live floci
// build rather than inferred from the plan output (see the issue and
// live/e2e/corpus-cncf-k8s-infra-aws-capa-ami/run.sh): CreateRole and
// GetRole both correctly return every tag, default_tags merged client-side
// by terraform-provider-aws before the request ever reaches the emulator.
// The gap is upstream of both. [providers.Configured.ImportResourceState]
// answers a bare identity with no configuration in hand, so a provider
// whose ReadResource distinguishes "explicitly declared" tags from
// "arrived through the provider's own default_tags" using only the prior
// state it is given sees an empty PriorState.tags and falls back to
// comparing raw tag VALUES against its default_tags config - misclassifying
// any tag the configuration also declares explicitly if it happens to
// duplicate a default_tags entry.
//
// Issues #395 and #376 are the same shape with a different provider
// quirk on the reading end. #395: hashicorp/aws's aws_ecs_service Read
// preserves the FORMAT (short "family:revision" vs full ARN) of whatever
// task_definition value it finds in PriorState, and falls back to the
// short form when PriorState carries none - confirmed directly (see
// TestReadImportedSeedsEveryNonComputedAttribute and this package's own
// build_seed_test.go): floci's DescribeServices always returns the full
// ARN on the wire, but choudoufu's import stub leaves task_definition
// null (ImportResourceState has no configuration to draw it from), so
// every stateless replan re-triggers the short-form fallback forever.
// #376: hashicorp/aws's aws_ecs_task_definition Read never sources
// track_latest or skip_destroy from the API at all - both are
// client-side-only arguments, confirmed by the issue - so a null
// PriorState comes back as the SDK's own zero-valued default (false),
// discarding whatever the configuration actually declared.
//
// Both are the identical defect: choudoufu has no persisted state, so
// [importAndRead] re-derives "prior state" through ImportResourceState on
// every single plan, and that stub is far barer than what an ordinary,
// state-backed OpenTofu run would have handed ReadResource - a real state
// file's PriorState carries the FULL, LAST-APPLIED value of every argument,
// not just the identity. This is what re-seeds the missing signal, for
// every argument where doing so cannot possibly be wrong.
//
// # Why "not Computed" is the right property, and the only one asked
//
// An attribute the schema marks Computed is one the protocol allows the
// PROVIDER to answer independently of configuration - that is what Computed
// means. An attribute that is Required, or Optional without Computed, is
// the opposite: nothing but configuration can ever produce its value, by
// the same protocol contract that lets [objchange.ProposedNew] always use
// the configured value for such an attribute regardless of what a prior
// read produced. So a persisted state file's PriorState for a non-Computed
// attribute is ALWAYS whatever was last configured - there is no other
// value it could hold - and handing [importAndRead] the CURRENT
// configuration's own value for such an attribute is not a guess, it is
// reconstructing exactly what state-backed OpenTofu's own PriorState
// already carries for it. It cannot mask real drift, because a
// non-Computed attribute has no drift a provider could introduce
// independent of configuration to begin with; the live system either
// matches what was configured or the type would not be able to keep
// Computed off that attribute at all.
//
// tags itself already satisfies this rule on nearly every AWS type ("tags"
// is Optional, never Computed - only "tags_all" carries the default_tags
// merge and is Computed), so the old tags-only version is strictly
// subsumed: dropping the tags_all gate here changes nothing about which
// resources' tags get seeded, only which OTHER attributes now do too.
//
// An Optional+Computed attribute is deliberately left out, even when
// configuration sets it to a concrete value: telling such an attribute's
// provenance apart from a value the provider might independently rewrite
// (a normalizing Read, a diff-suppress-only difference) needs more than a
// single schema flag can say, and issues #395 and #376 do not need that
// population to be fixed. A narrower, correct rule beats a broader,
// unproven one.
//
// # The population this reaches
//
// Measured against the AWS provider's own schema (6.59.0): every top-level,
// flat (no NestedType), non-WriteOnly attribute that is Required or
// Optional-without-Computed on every admitted resource type - hundreds of
// types beyond aws_ecs_service and aws_ecs_task_definition, because the
// shape ("an argument only configuration can ever set") is one of the most
// common in the schema, not an ECS peculiarity. Block-typed arguments
// (schema.Block.BlockTypes) are deliberately out of scope: they need their
// own nested-decode handling the way [residueEligibleBlock] gives residue,
// and neither #395 nor #376 needs it.
//
// # Per-attribute, not per-resource, and why
//
// Each eligible attribute is decoded with its OWN [hcldec.PartialDecode]
// call and its OWN [configs.StaticEvaluator.EvalContext], exactly as the
// original tags-only version did, and deliberately never combined into one
// multi-attribute decode: this estate's own aws_iam_role.this and
// aws_iam_openid_connect_provider.this resources have OTHER arguments
// referencing a data source that is never statically evaluable, and
// [configs.StaticEvaluator.EvalContext] fails outright the moment ANY of
// the references it was asked to resolve cannot be - which would turn one
// unresolvable sibling attribute into a lost seed for every OTHER,
// perfectly resolvable attribute on the same resource. A resource whose
// task_definition is a plain literal but whose desired_count references an
// unresolvable value must still get task_definition seeded.
//
// A resource whose eligible attribute is not statically evaluable at all -
// it references another resource's computed attribute the module-level
// evaluator has no repetition data for, or an each/count value this pass
// cannot resolve - simply does not appear in the returned map for that
// attribute: [importAndRead] falls back to ImportResourceState's own
// answer for it, exactly as before this existed. That is the same "refuse
// rather than guess" choice [PlanInstances] makes for the same reason.
// configMarks are the sensitivity marks that belong on the CONFIGURATION
// expression for one of this resource's flat attributes, whether or not
// that expression's VALUE could also be seeded - see [configuredAttrSeed]'s
// own doc comment for why the two are answered separately. [importAndRead]'s
// doc comment has GitHub issue #401 family 3, which this exists to close: a
// config-derived mark that never comes back on the projected prior is what
// turns a genuinely unchanged value into a perpetual sensitivity-only diff.
// Each entry's Path is relative to the whole resource object (attribute
// name first), ready to merge straight into [readImported]'s own
// schema-mark reconciliation with [combineValueMarks].
func configuredAttrsSeed(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, rc *configs.Resource, schema providers.Schema, dataSchemas map[string]providers.Schema) (seed map[string]cty.Value, configMarks []cty.PathValueMarks) {
	if eval == nil || rc == nil || schema.Block == nil {
		return nil, nil
	}
	identityAttrs := residueIdentityAttrs(schema)

	ident := configs.StaticIdentifier{
		Module:    modPath,
		Subject:   rc.Addr().String(),
		DeclRange: rc.DeclRange,
	}

	var out map[string]cty.Value
	var marks []cty.PathValueMarks
	for name, attr := range schema.Block.Attributes {
		if attr == nil || name == "tags" || identityAttrs[name] {
			// "tags" stays configuredTagsSeed's own name, with its own
			// default_tags-aware, tags_all-gated reasoning - seeding it
			// twice from two independent paths is exactly the drift a
			// single mechanism is supposed to prevent. Every identity
			// attribute ([residueIdentityAttrs]) is refused regardless of
			// its own Required/Optional/Computed shape (most identity
			// attributes are Computed and would already be excluded below,
			// but a client-named identity component need not be): seeding
			// one would put this mechanism in a position to influence
			// WHICH object a plan binds to, which is the one thing
			// HANDOFF.md's safety rule reserves for the record and the
			// marker alone.
			continue
		}
		if attr.WriteOnly || attr.NestedType != nil {
			continue
		}
		if !attr.Required && !attr.Optional {
			// Neither settable by configuration nor computed is not a real
			// schema shape, but this loop asks nothing of the schema it
			// cannot answer safely from these two flags alone. A purely
			// Computed attribute has no configuration expression at all -
			// nothing to seed a VALUE from and nothing to check for a MARK
			// either - so it is excluded from both questions here, not
			// just the value one.
			continue
		}
		val, localMarks, ok := configuredAttrSeed(ctx, eval, ident, rc, attr, name, dataSchemas)
		for _, pvm := range localMarks {
			marks = append(marks, cty.PathValueMarks{
				Path:  append(cty.GetAttrPath(name), pvm.Path...),
				Marks: pvm.Marks,
			})
		}
		if attr.Computed || !ok {
			// Optional+Computed is aws_instance.ami's own real shape
			// (hashicorp/aws 6.59.0) - settable, but the provider may
			// answer independently when configuration is silent, which is
			// why its VALUE was never seeded here even before this
			// function existed (issue #395/#376's own "never Computed"
			// rule). GitHub issue #401 family 3's bug was this same
			// exclusion applied a layer too high: Computed governs
			// whether the VALUE can be trusted, not whether the
			// CONFIGURATION EXPRESSION carries a sensitivity mark worth
			// recording - configuredAttrSeed already ran above and the
			// marks loop already captured whatever it found, so only the
			// seed map is skipped here.
			continue
		}
		if out == nil {
			out = make(map[string]cty.Value)
		}
		out[name] = val
	}
	return out, marks
}

// configuredAttrSeed is [configuredAttrsSeed]'s per-attribute decode, split
// out so a panic recovered from ONE attribute's decode (a provider plugin
// is a subprocess doing arbitrary work on a value this package built, the
// same hazard [PlanInstances]'s planOne guards) never loses every other
// attribute's seed on the same resource.
//
// It answers two INDEPENDENT questions, because they have different
// prerequisites. Whether the VALUE can be seeded needs a full, successful
// static evaluation - [Options.DataResults], when the attribute reads a
// data source, among everything else - and often cannot be answered at all
// (ok=false is the ordinary, common case this whole mechanism already
// tolerates). Whether the value is SENSITIVE by configuration is a much
// narrower question with a much cheaper answer for the one shape that
// matters here: a bare `attr = data.<type>.<name>.<field>` reference needs
// only that data source TYPE's own schema, which this package already has
// in hand for the SAME provider serving the resource being seeded, with no
// read and no [Options.DataResults] entry at all. [dataSensitivePath]
// answers that question; the returned marks reflect it even when ok is
// false, and a panic recovered below only clears val/ok, never marks - a
// crash in the VALUE half must not erase a fact the SCHEMA half already
// established with nothing but a schema map.
func configuredAttrSeed(ctx context.Context, eval *configs.StaticEvaluator, ident configs.StaticIdentifier, rc *configs.Resource, attr *configschema.Attribute, name string, dataSchemas map[string]providers.Schema) (val cty.Value, pvms []cty.PathValueMarks, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			val, ok = cty.NilVal, false
		}
	}()

	spec := hcldec.ObjectSpec{
		name: &hcldec.AttrSpec{Name: name, Type: attr.Type, Required: false},
	}
	traversals := hcldec.Variables(rc.Config, spec)

	if dataSensitivePath(traversals, dataSchemas) {
		pvms = []cty.PathValueMarks{{Marks: cty.NewValueMarks(marks.Sensitive)}}
	}

	refs, refDiags := lang.References(addrs.ParseRef, traversals)
	if refDiags.HasErrors() {
		return cty.NilVal, pvms, false
	}
	hclCtx, ctxDiags := eval.EvalContext(ctx, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, pvms, false
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}

	configVal, _, valDiags := hcldec.PartialDecode(rc.Config, spec, hclCtx)
	if valDiags.HasErrors() || configVal == cty.NilVal || configVal.IsNull() || !configVal.Type().HasAttribute(name) {
		return cty.NilVal, pvms, false
	}

	// Unmarked here, unconditionally, and BEFORE anything else reads the
	// value - see [configuredAttrsSeed]'s doc comment's history for why
	// (GitHub issue #287, TestConfiguredTagsSeedUnmarksASensitiveTagValue):
	// the seed goes straight into ReadResourceRequest.PriorState, and cty's
	// msgpack encoder refuses a marked value outright. Nothing sensitive is
	// lost by unmarking here, though: the paths this call captures on the
	// way off travel back to the caller as marks below - superseding the
	// schema-only guess above with the real, resolved answer, when the
	// static evaluator got far enough to have one - and [readImported]
	// puts them back on the object this package persists, alongside
	// whatever the schema itself marks.
	attrVal, localMarks := configVal.GetAttr(name).UnmarkDeepWithPaths()
	if len(localMarks) > 0 {
		pvms = localMarks
	}
	if attrVal.IsNull() || !attrVal.IsWhollyKnown() {
		return cty.NilVal, pvms, false
	}
	return attrVal, pvms, true
}

// dataSensitivePath reports whether any raw variable traversal a
// configuration expression makes reads an attribute a DATA SOURCE's OWN
// schema marks Sensitive - a purely static fact, checkable from the schema
// alone with no data ever read. It exists because [Options.DataResults] is
// scoped to what IDENTITY resolution demands (dataread.Analyze's own doc
// comment - "resolution is run... every data-source refusal it still
// raises names a newly demanded source"), so a data reference that feeds
// only an ordinary, non-identity attribute - aws_instance.ami reading
// data.aws_ssm_parameter.al2.value, corpus-alb-complete's own shape and
// GitHub issue #401 family 3's reproduction - is never read before a
// projection is built, and the VALUE genuinely can never be seeded. What
// it WOULD be sensitive needs no value at all: hashicorp/aws's
// aws_ssm_parameter data source marks "value" Sensitive unconditionally,
// regardless of which parameter it names or what that parameter holds.
//
// Only the one flat shape a plain attribute reference actually is - a
// traversal of data.<type>.<name>.<attr>, naming one top-level attribute
// of the data source's own result object directly - is recognized.
// Anything with a function call or a deeper path in between is a question
// [configuredAttrSeed]'s own value resolution already declines to answer
// for other reasons (it is not a bare reference), and guessing past that
// here would be exactly the kind of invented answer this package's own
// "refuse rather than guess" rule exists to rule out.
func dataSensitivePath(traversals []hcl.Traversal, dataSchemas map[string]providers.Schema) bool {
	for _, trav := range traversals {
		if len(trav) < 4 {
			continue
		}
		root, ok := trav[0].(hcl.TraverseRoot)
		if !ok || root.Name != "data" {
			continue
		}
		typeStep, ok := trav[1].(hcl.TraverseAttr)
		if !ok {
			continue
		}
		if _, ok := trav[2].(hcl.TraverseAttr); !ok {
			// The data instance's own name, unused beyond confirming the
			// traversal has the shape data.<type>.<name>.<attr> rather
			// than, say, an index into a for_each'd data block.
			continue
		}
		attrStep, ok := trav[3].(hcl.TraverseAttr)
		if !ok {
			continue
		}
		dsSchema, ok := dataSchemas[typeStep.Name]
		if !ok || dsSchema.Block == nil {
			continue
		}
		if a, ok := dsSchema.Block.Attributes[attrStep.Name]; ok && a != nil && a.Sensitive {
			return true
		}
	}
	return false
}

// configuredTagsSeed statically evaluates a taggable resource's own,
// AS-WRITTEN "tags" argument - before [stamp.Stamp] ever touches it - for
// GitHub issue #287 item 8: a stamped resource with a provider-level
// default_tags block showed a permanent, spurious "tags" in-place diff,
// re-adding the SAME keys on every single plan.
//
// The mechanism, verified directly against a live floci build rather than
// inferred from the plan output (see the issue and
// live/e2e/corpus-cncf-k8s-infra-aws-capa-ami/run.sh): CreateRole and
// GetRole both correctly return every tag, default_tags merged client-side
// by terraform-provider-aws before the request ever reaches the emulator.
// The gap is upstream of both. [providers.Configured.ImportResourceState]
// answers a bare identity with no configuration in hand, so a provider
// whose ReadResource distinguishes "explicitly declared" tags from
// "arrived through the provider's own default_tags" using only the prior
// state it is given sees an empty PriorState.tags and falls back to
// comparing raw tag VALUES against its default_tags config - which
// misclassifies any tag the configuration also declares explicitly, if it
// happens to duplicate a default_tags entry. The estate this was found
// against does exactly that: the caller's own `tags = var.tags` feeds BOTH
// the resource's tags argument AND, through the same variable, the
// provider block's default_tags map. A plain OpenTofu apply followed by a
// plain refresh never hits this: the state written at CREATE already
// carries the resource's own declared tags, so PriorState.tags is never
// empty going into a later ReadResource. choudoufu has no persisted state -
// every plan re-derives prior state through [importAndRead] - so it hits
// the ambiguity on every single plan, forever, for any stamped resource
// whose configuration redeclares a default_tags key. This is what re-seeds
// the missing signal.
//
// This function deliberately reads the SAME cfg [Build] was given, which is
// the configuration internal/command/live_plan.go's numbered pipeline
// resolves and projects at step 7, BEFORE step 8 (stamp.Stamp) rewrites a
// copy of it to add the marker entries - see that package's own doc
// comment, "The seam: configuration synthesis, before the plan runs":
// stamping mutates an in-memory copy, never a file, and never before a
// projection has already been built from the unstamped one. So the "tags"
// value this reads is exactly the author's own declaration, with no marker
// entries in it - and that is precisely the right value to seed with: the
// marker keys (tofu-estate, tofu-address, tofu-slot) never collide with a
// real default_tags entry, so the provider's own ambiguity never touches
// them either way. Only a key the configuration's OWN declaration happens
// to share with default_tags needs this signal, and that is exactly the
// key set this reads.
//
// Read from the schema - [markers.Taggable] plus a "tags_all" attribute,
// the AWS provider's transparent-tagging convention present on nearly
// every taggable AWS type - never from a type name list, so the fix
// reaches every type sharing that convention rather than the two this
// issue happened to name.
//
// A resource whose "tags" argument is not statically evaluable - it
// references another resource's computed attribute, or an each/count value
// this module-level evaluator has no repetition data for - is left alone:
// the second return is false and [importAndRead] falls back to
// ImportResourceState's own answer, exactly as it did before this existed.
// That is the same "refuse rather than guess" choice [PlanInstances] makes
// for the same reason.
//
// It decodes ONLY the "tags" attribute, deliberately never the resource's
// whole config the way [PlanInstances]'s planOne does: this estate's own
// aws_iam_role.this and aws_iam_openid_connect_provider.this resources have
// OTHER arguments referencing a data source
// (data.aws_iam_policy_document.this, data.aws_partition.current) that are
// never statically evaluable, and a whole-block decode against
// [configschema.Block.DecoderSpec] fails outright the moment any one
// attribute cannot resolve - hcl's Body.Content, which backs
// [hcl.Body.Content]-based decoding, errors on a body it was not given a
// schema entry for just as readily as it errors on an unresolvable
// reference. A single-attribute spec plus [hcldec.PartialDecode] asks only
// about "tags" and tolerates every sibling argument the schema does not
// mention, so a resource whose OTHER arguments are dynamic still gets its
// (fully static) tags seeded.
func configuredTagsSeed(ctx context.Context, eval *configs.StaticEvaluator, modPath addrs.Module, rc *configs.Resource, schema providers.Schema) (val cty.Value, ok bool) {
	if eval == nil || rc == nil || schema.Block == nil {
		return cty.NilVal, false
	}
	if !markers.Taggable(schema.Block) {
		return cty.NilVal, false
	}
	if attr, has := schema.Block.Attributes["tags_all"]; !has || attr == nil {
		// Not the default_tags-merging convention: nothing to disambiguate.
		return cty.NilVal, false
	}
	tagsAttr := schema.Block.Attributes["tags"]
	if tagsAttr == nil {
		return cty.NilVal, false
	}

	// A provider plugin is a subprocess doing arbitrary work on a value
	// this package built; [PlanInstances]'s planOne guards the same decode
	// call the same way, for the same reason.
	defer func() {
		if rec := recover(); rec != nil {
			val, ok = cty.NilVal, false
		}
	}()

	spec := hcldec.ObjectSpec{
		"tags": &hcldec.AttrSpec{Name: "tags", Type: tagsAttr.Type, Required: false},
	}

	ident := configs.StaticIdentifier{
		Module:    modPath,
		Subject:   rc.Addr().String(),
		DeclRange: rc.DeclRange,
	}
	refs, refDiags := lang.References(addrs.ParseRef, hcldec.Variables(rc.Config, spec))
	if refDiags.HasErrors() {
		return cty.NilVal, false
	}
	hclCtx, ctxDiags := eval.EvalContext(ctx, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, false
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}

	configVal, _, valDiags := hcldec.PartialDecode(rc.Config, spec, hclCtx)
	if valDiags.HasErrors() || configVal == cty.NilVal || configVal.IsNull() {
		return cty.NilVal, false
	}
	if !configVal.Type().HasAttribute("tags") {
		return cty.NilVal, false
	}
	// Unmarked here, unconditionally, and BEFORE anything else reads the
	// value.
	//
	// The seed goes straight into ReadResourceRequest.PriorState by way of
	// [withSeededTags] and [importAndRead], and cty's msgpack encoder refuses
	// a marked value outright ("value has marks, so it cannot be serialized").
	// Unlike [planOne]'s own decode, which loses one resource when that
	// happens, this one loses the ESTATE: the refusal arrives as a
	// ReadResource error diagnostic, importAndRead turns it into a "Cannot
	// read for projection" error and statusFailed, and an error in b.diags
	// fails BuildWith - so `live-plan` refuses for every resource, not just
	// the tagged one. The gate above is [markers.Taggable] plus a "tags_all"
	// attribute, which nearly every AWS type satisfies, so any estate writing
	// `tags = { Owner = var.owner }` for a `sensitive = true` owner hits it.
	//
	// The mark arrives in two places and one unmark closes both.
	// `tags = { Owner = var.owner }` leaves it on the map's ELEMENT, because
	// an object constructor does not hoist its elements' marks;
	// `tags = var.tags` marks the CONTAINER. Neither was caught: IsNull is
	// indifferent to a mark, and cty's own IsWhollyKnown unmarks before it
	// recurses (cty/value.go:83), so both cleared every test below and
	// travelled on.
	//
	// Unmarked rather than refused, and that is the semantic claim rather than
	// the convenient one: this seed exists to tell the provider's own
	// ReadResource which raw tags the configuration declares explicitly, as
	// opposed to which arrived through the provider's default_tags (see this
	// function's doc comment and GitHub issue #287 item 8). It stands in for
	// what a persisted state file's PriorState.tags would have shown - and a
	// state file records that value in the clear too, with its sensitivity
	// carried alongside as a path rather than inside the value. So the
	// plaintext is what the provider is owed, and it is a value this same
	// provider is told in the clear on any apply that writes the tag.
	//
	// Nothing sensitive is retained from here. The seed is written into the
	// import stub only for the duration of the ReadResource call; what the
	// projection persists is that call's own ANSWER, marked from the schema by
	// importAndRead's own schema.Block.ValueMarks. Carrying the configuration's
	// marks onto the seed instead would put them on the stub rather than on
	// what comes back, which is why the paths are dropped here rather than
	// combined back in the way [remarkPlanned] does for a value that IS
	// returned to a caller.
	//
	// Found by the audit of #343/#344's fix to builder.normalizeIdentityAttrs,
	// which is the same defect two call sites over; pinned by
	// TestConfiguredTagsSeedUnmarksASensitiveTagValue and
	// TestProjectionSurvivesASensitiveTagValue.
	tagsVal, _ := configVal.GetAttr("tags").UnmarkDeep()
	if tagsVal.IsNull() || !tagsVal.IsWhollyKnown() {
		return cty.NilVal, false
	}
	return tagsVal, true
}

// withSeededTags returns a copy of v with its "tags" attribute replaced by
// seed, when v is an object with a "tags" attribute of the exact same type
// seed has. Any mismatch - v not an object, no "tags" attribute, a
// different type - returns v unchanged and false: this is a best-effort
// seed for [configuredTagsSeed]'s ambiguity, not a correctness requirement,
// and a provider whose ImportResourceState returns a differently-shaped
// stub than expected should be read exactly as it always was.
func withSeededTags(v cty.Value, seed cty.Value) (cty.Value, bool) {
	if v == cty.NilVal || v.IsNull() || !v.Type().IsObjectType() {
		return v, false
	}
	ty := v.Type()
	if !ty.HasAttribute("tags") || !seed.Type().Equals(ty.AttributeType("tags")) {
		return v, false
	}
	attrs := make(map[string]cty.Value, len(ty.AttributeTypes()))
	for name := range ty.AttributeTypes() {
		attrs[name] = v.GetAttr(name)
	}
	attrs["tags"] = seed
	return cty.ObjectVal(attrs), true
}

// withSeededAttrs is [withSeededTags]'s general form: every FLAT name in
// seed that v's object type both has and agrees on the type of is
// overlaid onto v; a name seed does not name, or whose type disagrees, is
// left exactly as the stub already had it - the same best-effort, never-a-
// correctness-requirement stance [withSeededTags] documents, for the same
// reason (a provider whose ImportResourceState returns a differently-
// shaped stub than expected is read exactly as it always was for the
// attributes that mismatch).
//
// A path-keyed entry ([isResiduePathKey], produced only by
// [builder.residueSeedFor]'s own nested half) is not a flat name and is
// routed through [setResiduePathValues] instead, unconditionally
// (requireEmpty=false) - the same reasoning the flat half already applies
// to every entry here: v is an import stub with no configuration in hand,
// not a live answer this seed would be shadowing, so there is nothing to
// protect by refusing to overwrite a null it already holds.
func withSeededAttrs(v cty.Value, seed map[string]cty.Value) (cty.Value, bool) {
	if v == cty.NilVal || v.IsNull() || !v.Type().IsObjectType() || len(seed) == 0 {
		return v, false
	}
	ty := v.Type()
	attrs := make(map[string]cty.Value, len(ty.AttributeTypes()))
	for name := range ty.AttributeTypes() {
		attrs[name] = v.GetAttr(name)
	}
	seeded := false
	var pathSeed map[string]cty.Value
	for name, val := range seed {
		if isResiduePathKey(name) {
			if pathSeed == nil {
				pathSeed = make(map[string]cty.Value, len(seed))
			}
			pathSeed[name] = val
			continue
		}
		if !ty.HasAttribute(name) || !val.Type().Equals(ty.AttributeType(name)) {
			continue
		}
		attrs[name] = val
		seeded = true
	}
	result := v
	if seeded {
		result = cty.ObjectVal(attrs)
	}
	if len(pathSeed) > 0 {
		if pathResult, n := setResiduePathValues(result, pathSeed, false); n > 0 {
			result = pathResult
			seeded = true
		}
	}
	if !seeded {
		return v, false
	}
	return result, true
}

// importAndRead is the whole provider conversation for one instance, and
// is the reason this package exists rather than calling into a graph walk:
// ImportResourceState to turn an identity into a stub object, then
// ReadResource to fill that stub in from the live system.
//
// target is the form the import is asked in - an identity object or an
// import-ID string, never both, see [importTarget]. importID is carried
// alongside whichever form was chosen because it is what every sentence here
// names the resource by: an operator reading "no aws_subnet exists with
// identity …" needs the string whether or not the wire carried it.
//
// It mirrors graphNodeImportState/graphNodeImportStateSub and
// NodeAbstractResourceInstance.refresh in internal/tofu, minus hooks, the
// evaluation context, and the already-in-state check, and with one
// deliberate semantic difference: where import treats a nonexistent remote
// object as a hard error, a projection treats it as an ordinary absence.
//
// attrsSeed is [configuredAttrsSeed]'s answer for this instance, applied to
// the stub object ImportResourceState returns, BEFORE ReadResource sees it -
// see the seeding step below for why this is the one place in the whole
// conversation it can go.
//
// The second return is the exact object handed to ReadResource as
// PriorState - the import stub, attribute-seeded but otherwise untouched by
// any live read. [builder.fillResidueFor] needs it for GitHub issue #393: an
// Optional, non-Computed SDKv2 attribute that ImportResourceState seeds with
// the provider's own internal schema Default (invisible over the plugin
// protocol, so nothing in [providers.Schema] can name it) survives
// ReadResource completely unread whenever the provider does not source that
// attribute from the remote at all, which for a residue candidate is true by
// construction (see [classifyResidue]'s doc comment). The value that comes
// back is then bit-for-bit the same value that went in, and comparing the
// two is the only way to tell that apart from a value ReadResource actually
// produced - the schema itself carries no such signal to ask instead.
func importAndRead(ctx context.Context, provider providers.Interface, schema providers.Schema, typeName string, target providers.ImportTarget, importID string, identityValues map[string]string, attrsSeed map[string]cty.Value, configMarks []cty.PathValueMarks) (*states.ResourceInstanceObject, cty.Value, materializeStatus, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if !target.IsIdentityBased() && !target.IsIDBased() {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Empty import identity",
			fmt.Sprintf("Nothing was computed as the import identity for a %s: no identity object and no import ID. For a type identified by several attributes with no separator between them, the identity object is the only form there is (see internal/live/identity's IdentityObjectOnly), so an identity the provider's schema would not accept leaves nothing to import by - which is refused here rather than approximated with a string.", typeName),
		))
		return nil, cty.NilVal, statusFailed, diags
	}

	importResp := provider.ImportResourceState(ctx, providers.ImportResourceStateRequest{
		TypeName: typeName,
		Target:   target,
	})
	if importResp.Diagnostics.HasErrors() {
		if ok, detail := notFoundDiagnostics(importResp.Diagnostics); ok {
			// Some providers answer "there is no such object" as an error
			// diagnostic out of ImportResourceState rather than an empty
			// ImportedResources list - aws_lambda_permission is a confirmed
			// instance, whose import lookup calls GetPolicy on the
			// *function* and gets ResourceNotFoundException back when the
			// function itself does not exist yet either (issue #297). That
			// is still an ordinary absence, not a provider that could not
			// answer, so it takes the same path an empty list or a null
			// read result already takes below.
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Warning,
				"Import reported absence as an error",
				fmt.Sprintf(
					"Looking up the %s with identity %q failed with what reads as a not-found response rather than a genuine error: %s. Treating it as an ordinary absence.",
					typeName, importID, detail,
				),
			))
			return nil, cty.NilVal, statusAbsent, diags
		}
		if ok, detail := noimporter.Diagnostics(importResp.Diagnostics); ok {
			// Not a provider erroring - the opposite. The provider is
			// correctly answering that ImportResourceState is not
			// implemented for this type at all, a fact fixed in the
			// provider's own Go code and never going to change no matter
			// what identity this run asks with or how many times. Reusing
			// "Cannot import for projection"'s wording here would claim
			// a transient failure this run could retry past.
			//
			// Retry, though, is not the only thing "no identity or retry
			// changes" leaves off the table - a DIFFERENT mechanism than
			// the one that just failed does. ImportResourceState is not the
			// only way to obtain a stub for ReadResource to fill in: it is
			// this package's only source for one when the identity has to
			// be discovered, but for a type this run already knows the
			// identity of BY CONFIGURATION - identity.Derivable admitted it
			// on nameability alone, which is why materialize() ever reached
			// this call with a real importID at all - the stub
			// ImportResourceState would have produced (near-null, with only
			// the identity attribute(s) set - the same shape
			// ImportStatePassthroughContext's own generic implementation
			// always builds) is one this run can build itself, with nothing
			// about its shape depending on the missing RPC. See
			// [noimporter.SynthesizeStub]. Only when that has nothing to
			// build from - no named identity attribute values at all, the
			// case a marker-swept or record-located identity is in, which
			// [identity.LocatedType]'s own condition 0 already keeps a
			// NotImportable type out of - does this fall back to the
			// refusal below, at the same severity, so the plan still stops
			// rather than risk proposing a create for an object it cannot
			// verify one way or the other. See [noimporter.Diagnostics] for
			// the population this reaches.
			if stub, stubOK := noimporter.SynthesizeStub(schema, identityValues); stubOK {
				log.Printf("[TRACE] projection: %s has no classic Importer; synthesizing an import stub from its own resolved identity instead of refusing", typeName)
				obj := &states.ResourceInstanceObject{Status: states.ObjectReady, Value: stub}
				return readImported(ctx, provider, schema, typeName, importID, obj, attrsSeed, configMarks, diags)
			}
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Resource type has no classic Importer",
				fmt.Sprintf(
					"The %s with identity %q cannot be projected: %s. This is not the provider erroring - it is answering that ImportResourceState is not implemented for this type at all, a fixed property of the provider's own code that no identity or retry changes. A type in this position is admitted for naming and reference purposes only (see identity.Derivable and issue #331); it cannot be read back through a live plan, so this run refuses rather than propose a create for an object it cannot verify one way or the other.",
					typeName, importID, detail,
				),
			))
			return nil, cty.NilVal, statusFailed, diags
		}
		// The provider could not answer the question. That is different
		// from answering "there is no such object", which is either an
		// empty ImportedResources or a null object out of the read below.
		diags = diags.Append(importResp.Diagnostics.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot import for projection",
			fmt.Sprintf(
				"The provider failed while looking up the %s with identity %q. A projection cannot be built while a provider is erroring, because the resulting plan would propose creating resources that may already exist.",
				typeName, importID,
			),
		)))
		return nil, cty.NilVal, statusFailed, diags
	}
	diags = diags.Append(importResp.Diagnostics)

	imported, extras := pickImported(importResp.ImportedResources, typeName)
	if imported == nil {
		// The provider returned nothing at all for this identity, which is
		// how several resource types report "no such object" without an
		// error.
		return nil, cty.NilVal, statusAbsent, diags
	}
	for _, extra := range extras {
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			"Ignoring an additional imported object",
			fmt.Sprintf(
				"Importing the %s with identity %q also produced an object of type %q. A projection has no configuration address to file that under, so it is not included.",
				typeName, importID, extra,
			),
		))
	}

	obj := imported.AsInstanceObject()
	if obj.Value == cty.NilVal || obj.Value.IsNull() {
		return nil, cty.NilVal, statusAbsent, diags
	}

	return readImported(ctx, provider, schema, typeName, importID, obj, attrsSeed, configMarks, diags)
}

// readImported is [importAndRead]'s shared tail: ReadResource against obj,
// the stub either ImportResourceState produced or
// [noimporter.SynthesizeStub] built in its place, then everything a
// projection owes the value that comes back. Split out so the synthesized
// path can reach the exact same attribute-seeding, sensitivity-marking and
// conformance-checking rules an ordinarily-imported instance already gets,
// with no second copy to drift from the first.
func readImported(ctx context.Context, provider providers.Interface, schema providers.Schema, typeName, importID string, obj *states.ResourceInstanceObject, attrsSeed map[string]cty.Value, configMarks []cty.PathValueMarks, diags tfdiags.Diagnostics) (*states.ResourceInstanceObject, cty.Value, materializeStatus, tfdiags.Diagnostics) {
	// GitHub issue #287 item 8 (tags), #395 and #376 (every other
	// non-Computed attribute - see [configuredAttrsSeed]'s doc comment).
	// ImportResourceState commonly leaves a non-Computed argument null or
	// zero-valued on the stub it returns - it has no configuration to
	// consult, only the identity it was given - and a provider whose
	// ReadResource behavior depends on what PriorState already held for
	// such an argument (preserving a tag's provenance, preserving a
	// format, or simply never sourcing the argument from the remote at
	// all) then has no signal at all. A resource with real, persisted
	// state never hits this - its state already carries the argument's
	// own last-applied value, so an ordinary refresh's PriorState carries
	// the same signal a live create's response would. choudoufu has no
	// such state: every plan re-derives prior state through exactly this
	// call, so a projection that skipped this step would hit the
	// ambiguity on every single plan, permanently. Seeding PriorState with
	// what the configuration actually declares, for every attribute where
	// that is statically known and safe (never Computed), makes this call
	// see what a genuinely persisted state would have shown.
	if seeded, ok := withSeededAttrs(obj.Value, attrsSeed); ok {
		obj.Value = seeded
	}
	if len(attrsSeed) > 0 {
		if seeded, ok := withSeededAttrs(obj.Value, attrsSeed); ok {
			obj.Value = seeded
		}
	}

	// The exact PriorState ReadResource is about to see, captured before the
	// call so a caller can tell "the read confirmed this value" apart from
	// "the read never touched this value" - see the doc comment above.
	importStub := obj.Value

	readResp := provider.ReadResource(ctx, providers.ReadResourceRequest{
		TypeName:   typeName,
		PriorState: obj.Value,
		Private:    obj.Private,
		// A null of the dynamic pseudo-type, not the zero cty.Value: the
		// plugin client marshals ProviderMeta whenever the provider
		// declares a provider_meta schema (the AWS provider does), and
		// marshalling starts with a conformance check that panics on a
		// value with no type at all. This is the same value
		// NodeAbstractResourceInstance.providerMetas defaults to. A
		// projection has no provider_meta block to evaluate, so null is
		// also the correct answer.
		ProviderMeta:  cty.NullVal(cty.DynamicPseudoType),
		PriorIdentity: obj.Identity,
	})
	if readResp.Diagnostics.HasErrors() {
		diags = diags.Append(readResp.Diagnostics.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"Cannot read for projection",
			fmt.Sprintf(
				"The provider failed while refreshing the %s imported with identity %q.",
				typeName, importID,
			),
		)))
		return nil, cty.NilVal, statusFailed, diags
	}
	diags = diags.Append(readResp.Diagnostics)

	if readResp.NewState == cty.NilVal {
		// Not reachable over the plugin RPC channel, but reachable from a
		// sloppy in-process provider, and a panic here would be a poor
		// error message.
		diags = diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			"No state returned by the provider",
			fmt.Sprintf("Reading the %s imported with identity %q produced no object at all, not even a null one. This is a bug in the provider.", typeName, importID),
		))
		return nil, cty.NilVal, statusFailed, diags
	}
	if readResp.NewState.IsNull() {
		// The ordinary "it does not exist" answer.
		return nil, cty.NilVal, statusAbsent, diags
	}

	if errs := readResp.NewState.Type().TestConformance(schema.Block.ImpliedType()); len(errs) > 0 {
		for _, err := range errs {
			diags = diags.Append(tfdiags.Sourceless(
				tfdiags.Error,
				"Provider produced an invalid object",
				fmt.Sprintf(
					"Reading the %s imported with identity %q produced a value that does not conform to the provider's own schema: %s. This is a bug in the provider.",
					typeName, importID, tfdiags.FormatError(err),
				),
			))
		}
		return nil, cty.NilVal, statusFailed, diags
	}

	newVal := objchange.NormalizeObjectFromLegacySDK(readResp.NewState, schema.Block)
	if !newVal.RawEquals(readResp.NewState) {
		log.Printf("[WARN] projection: provider produced an invalid new value containing null blocks for %s %q", typeName, importID)
	}

	// Sensitivity declared by the schema has to be carried on the value,
	// because that is where the plan renderer looks for it.
	//
	// This line is the concrete-cloud half of what a skipped refresh owes the
	// plan, and it is the same call upstream's own refresh ends with
	// (internal/tofu/node_resource_abstract_instance.go's
	// combinePathValueMarks(priorPaths, schema.Block.ValueMarks(...)) - there
	// is no priorPaths here, because newVal came off the wire, where the
	// plugin protocol has nowhere to put a mark). GitHub issue #343 read this
	// as missing, having looked at builder.materialize's encode rather than at
	// what fills the object it encodes; the issue closed on that finding
	// rather than on a change, and
	// TestMaterializeMarksASensitiveAttributeFromTheSchema is the test that
	// was genuinely missing. Deleting this line turns that test red, which is
	// how the reading was settled.
	//
	// configMarks is the OTHER half, GitHub issue #401 family 3: a mark that
	// came from the CONFIGURATION expression rather than from this type's own
	// schema - the common case is an argument set from a Sensitive attribute
	// of a data source, aws_instance.ami reading
	// data.aws_ssm_parameter.*.value being the estate that found it, since
	// that data source's own "value" is unconditionally Sensitive in every
	// provider version regardless of what it names, and nothing about "ami"
	// is sensitive on its own. configuredAttrsSeed captured those paths off
	// the config value before stripping them for the ReadResource round
	// trip (a marked value cannot cross the plugin channel, so this is the
	// only point they can be recovered at all); this is where they go back
	// on, exactly as internal/tofu's graphNodeImportStateSub.Execute's own
	// "Insert marks from configuration" step does for `choudoufu import`
	// upstream. Without
	// it, the CONFIG-evaluated side of every later plan carries the mark
	// (the ordinary dynamic evaluator propagates it same as this one did)
	// and this projected PRIOR side never does, so a value that is byte-for-
	// byte unchanged plans as an update, forever, on every single run.
	if marks := combineValueMarks(schema.Block.ValueMarks(newVal, nil, nil), configMarks); len(marks) > 0 {
		newVal = newVal.MarkWithPaths(marks)
	}

	return &states.ResourceInstanceObject{
		Status:   states.ObjectReady,
		Value:    newVal,
		Private:  readResp.Private,
		Identity: readResp.NewIdentity,
	}, importStub, statusMaterialized, diags
}

// pickImported selects the imported object that belongs at the address
// being materialized, and reports the type names of any others. The import
// protocol lets a provider return several related objects from one call;
// tofu import files them under synthesized addresses, but a projection is
// building state for addresses that already exist in configuration, so
// there is nowhere to put the extras.
func pickImported(imported []providers.ImportedResource, typeName string) (*providers.ImportedResource, []string) {
	var chosen *providers.ImportedResource
	var extras []string
	for i := range imported {
		ir := imported[i]
		switch {
		case ir.TypeName == typeName && chosen == nil:
			chosen = &imported[i]
		case ir.TypeName == "" && chosen == nil:
			// Some providers leave the type name off when there is only
			// one object and it is obviously the requested type.
			chosen = &imported[i]
		default:
			name := ir.TypeName
			if name == "" {
				name = typeName
			}
			extras = append(extras, name)
		}
	}
	return chosen, extras
}

// providerFor is the provider configuration one instance is read through:
// the one its resource block names, or - for an instance whose block is gone
// - the one the run supplied, falling back to the provider the resource type
// implies in the root module.
//
// For an undeclared instance, [Options.UndeclaredProviders] is checked
// first, keyed by addr: an estate whose managed resources span more than
// one provider configuration (issue #69) attributes each undeclared
// instance to the specific provider configuration whose sweep found it,
// because that account and region is where the resource actually lives.
// [Options.UndeclaredProvider] is the fallback for whichever undeclared
// instances the map does not name (every one of them, for a single-provider
// caller, which is what keeps this byte-identical to before issue #69). The
// implied-provider rule below ("aws_vpc" means the module's "aws"
// provider), with no alias, is the last resort when neither says anything:
// an alias is a property of the resource block and the block is what is
// missing, so a deleted block whose provider this run never listed through
// at all is the one case nothing here can serve, and it reports rather than
// reads through the wrong account.
func (b *builder) providerFor(rc *configs.Resource, modPath addrs.Module, typeName string, addr addrs.AbsResourceInstance) (addrs.AbsProviderConfig, bool) {
	if rc != nil {
		if modCfg, ok := identity.ConfigForModule(b.cfg, addr.Module); ok && modCfg.Module != nil {
			return providerConfigAddr(modCfg, rc), true
		}
		// materialize's own identity.ConfigForModule(b.cfg, addr.Module)
		// lookup is what produced rc in the first place, so this branch is
		// unreachable in practice; it exists only so an internal
		// inconsistency degrades to the resource's own local address
		// anchored at root rather than failing the whole plan.
		return addrs.AbsProviderConfig{Module: addrs.RootModule, Provider: rc.Provider, Alias: rc.ProviderConfigAddr().Alias}, true
	}
	if p, ok := b.opts.UndeclaredProviders[addr.String()]; ok && p.Provider.Type != "" {
		return p, true
	}
	if b.opts.UndeclaredProvider.Provider.Type != "" {
		return b.opts.UndeclaredProvider, true
	}
	modCfg, ok := identity.ConfigForModule(b.cfg, modPath.UnkeyedInstanceShim())
	if !ok || modCfg.Module == nil {
		return addrs.AbsProviderConfig{}, false
	}
	implied := modCfg.Module.ImpliedProviderForUnqualifiedType(impliedProviderName(typeName))
	if implied.Type == "" {
		return addrs.AbsProviderConfig{}, false
	}
	return addrs.AbsProviderConfig{Module: modPath, Provider: implied}, true
}

// impliedProviderName is the local provider name a resource type implies:
// everything before the first underscore, which is the rule the configuration
// loader uses for a resource block with no provider argument.
func impliedProviderName(typeName string) string {
	if i := strings.Index(typeName, "_"); i > 0 {
		return typeName[:i]
	}
	return typeName
}

// dependencies is the config-level dependency set of a resource block: the
// managed resources its arguments refer to. The plan engine uses these for
// destroy ordering when a resource's configuration is gone, which is
// exactly the case a projection has to survive.
//
// An instance with no resource block gets none, and that is a real cost
// stated rather than papered over: dependency order for a resource whose
// configuration is gone is exactly what a state file remembers and a
// projection cannot. Destroying it happens in whatever order the graph
// derives from the resources that do have configuration. In practice the
// cases that matter are handled by the cloud itself, which refuses to delete
// a VPC that still has a subnet in it, and a refusal is a legible error
// rather than a silent wrong order.
func (b *builder) dependencies(rc *configs.Resource, modPath addrs.Module, schema providers.Schema) []addrs.ConfigResource {
	key := modPath.String() + "\x00" + rc.Addr().String()
	if deps, ok := b.depsByType[key]; ok {
		return deps
	}

	self := rc.Addr()
	seen := make(map[string]addrs.ConfigResource)

	// Reference errors are not reported here: a body that cannot be walked
	// for references is a configuration error, and it is validation's job
	// to say so with source ranges. Recording no dependency is the safe
	// answer for a structure that is thrown away at the end of the run.
	refs, _ := lang.ReferencesInBlock(addrs.ParseRef, rc.Config, schema.Block)
	dependsOn, _ := lang.References(addrs.ParseRef, rc.DependsOn)
	refs = append(refs, dependsOn...)
	for _, ref := range refs {
		var res addrs.Resource
		switch sub := ref.Subject.(type) {
		case addrs.Resource:
			res = sub
		case addrs.ResourceInstance:
			res = sub.Resource
		default:
			continue
		}
		if res.Mode != addrs.ManagedResourceMode {
			continue
		}
		if res.Equal(self) {
			continue
		}
		cr := res.InModule(modPath)
		seen[cr.String()] = cr
	}

	keys := make([]string, 0, len(seen))
	for k := range seen {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	deps := make([]addrs.ConfigResource, 0, len(keys))
	for _, k := range keys {
		deps = append(deps, seen[k])
	}
	if len(deps) == 0 {
		deps = nil
	}
	b.depsByType[key] = deps
	return deps
}

// recordEnvelopeVersion appends addr's kind=identity envelope version to
// b.envelopeVersions, deduplicated by address. Located, residue and
// provisioned data now share one physical key, so a located instance's
// materialize() call can have all three of materializeLocated,
// fillResidueFor and applyProvisionedTaint read that identical key/version
// pair in the same pass; without the dedup here, write-back's expected
// version list would carry the same address two or three times over
// (harmlessly identical values, but [Result.EnvelopeVersions]'s own contract
// is one entry per address).
func (b *builder) recordEnvelopeVersion(addr addrs.AbsResourceInstance, version string) {
	key := addr.String()
	if b.envelopeVersionAddrs[key] {
		return
	}
	b.envelopeVersionAddrs[key] = true
	b.envelopeVersions = append(b.envelopeVersions, RecordVersion{Addr: addr, Version: version})
}

func (b *builder) omit(addr addrs.AbsResourceInstance, reason Reason, detail, cause string) {
	key := addr.String()
	if _, exists := b.omitted[key]; exists {
		return
	}
	o := Omission{Addr: addr, Reason: reason, Detail: detail}
	b.omitted[key] = o
	b.omissionList = append(b.omissionList, o)
	b.causes[key] = cause
}

// omitFailedCause is [builder.omitFailed]'s cause clause, named so that
// [readTerminal] - which records what a prepared read decided so that
// [builder.materialize] can apply it in loop order rather than at plan time -
// can spell the identical omission without a second copy of the sentence.
const omitFailedCause = "reading it from the provider failed."

// omitFailed is the common case: an omission that also produced an error
// diagnostic, so the detail is already written and the cause is the same
// for all of them.
func (b *builder) omitFailed(addr addrs.AbsResourceInstance, detail string) {
	b.omit(addr, ReasonFailed, detail, omitFailedCause)
}

func needsDiscoveryDetail(r identity.Resolution) string {
	return "No import identity exists for this instance: " + discoveryReason(r) + " Marker discovery will find it; until then the plan will propose creating it."
}

func needsDiscoveryCause(r identity.Resolution) string {
	return strings.TrimSuffix(discoveryReason(r), ".") + ", so only marker discovery can find it."
}

func discoveryReason(r identity.Resolution) string {
	reason := r.Reason
	if reason == "" {
		reason = "its identity is assigned by the provider and appears nowhere in configuration."
	}
	if !strings.HasSuffix(reason, ".") {
		reason += "."
	}
	return reason
}

// providerConfigAddr is the absolute provider configuration a resource
// block uses: [providerscope.ResolveResource] walking every ancestor module
// call's `providers = {...}` mapping between modCfg (the static module the
// block itself is declared in) and the root, honouring an aliased mapping
// instead of ignoring it. GitHub issue #188; the resolution core is
// internal/live/providerscope, built and tested separately from this
// wiring.
func providerConfigAddr(modCfg *configs.Config, rc *configs.Resource) addrs.AbsProviderConfig {
	return providerscope.ResolveResource(modCfg, rc)
}

// attrString reads one attribute out of a materialized object's value and
// renders it as the string an import identity needs, refusing anything
// that would silently produce a wrong identity.
func attrString(obj cty.Value, attr string) (string, error) {
	if obj == cty.NilVal || obj.IsNull() {
		return "", fmt.Errorf("the parent's object is null")
	}
	ty := obj.Type()
	if !ty.IsObjectType() || !ty.HasAttribute(attr) {
		return "", fmt.Errorf("the parent's object has no attribute %q", attr)
	}
	val, marks := obj.GetAttr(attr).Unmark()
	if len(marks) > 0 {
		return "", fmt.Errorf("the value of %q is marked (sensitive or ephemeral) and must not be composed into an import identity", attr)
	}
	if !val.IsKnown() {
		return "", fmt.Errorf("the value of %q is unknown", attr)
	}
	if val.IsNull() {
		return "", fmt.Errorf("the value of %q is null", attr)
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return "", fmt.Errorf("the value of %q is not usable as a string: %w", attr, err)
	}
	return str.AsString(), nil
}
