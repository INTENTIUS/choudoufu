// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/convert"
	"github.com/zclconf/go-cty/cty/gocty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/live/providerscope"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Resolve classifies every managed resource instance in the given
// configuration, using only the configuration itself: no provider process,
// no state, no cloud reads.
//
// The returned Result holds one Resolution per instance that could be
// classified. An instance that could not be classified is absent from the
// Result and has at least one error diagnostic explaining why; callers must
// treat error diagnostics as fatal for the run, since a projection built
// from a partial identity map would plan to create resources that already
// exist.
//
// Input variable values come from the configuration's own static module
// call, i.e. whatever the caller passed to the loader, falling back to
// declared defaults.
func Resolve(ctx context.Context, cfg *configs.Config) (*Result, tfdiags.Diagnostics) {
	return ResolveWith(ctx, cfg, Context{})
}

// Context is everything a caller may tell resolution about the world outside
// the configuration. Every field is optional and the zero value is what
// [Resolve] passes: nothing here is ever fetched, discovered, or guessed, and
// a caller with none of it gets the same answers this package has always
// given.
//
// The two fields are the same shape of escape hatch for the same reason. A
// configuration does not say which account it will be applied to, and it does
// not carry the provider's account of what identifies each resource type;
// both live behind a running provider, and resolution runs in front of one.
// So each is handed in by a caller that already has it, and its absence is an
// ordinary answer rather than an error.
type Context struct {
	// Cloud is the account and region the run is against. See
	// [CloudContext].
	Cloud CloudContext

	// Schemas are the provider's managed resource type schemas, keyed by
	// type name, as GetProviderSchema returns them. They let a type absent
	// from [DefaultTable] resolve anyway, when the provider's own resource
	// identity schema describes it completely enough. See
	// [SynthesizeTypeIdentity] for the rule and for what it refuses.
	//
	// Nil is the default and means the hand table is the whole of what this
	// run knows, which is what every caller running before a plugin has
	// started passes.
	Schemas map[string]providers.Schema

	// DataResults are the values the pre-resolution data-read phase read
	// (issue #179), keyed by the data resource instance's absolute address
	// ([addrs.AbsResourceInstance.String]). They let an identity argument, a
	// count or a for_each that reads a data source resolve against the
	// provider's own answer instead of refusing as non-static: the static
	// evaluator permits and answers exactly the references these cover, and
	// nothing else changes.
	//
	// Nil is the default and means no phase ran, which is what every caller
	// running before a provider has started passes; every data-source
	// reference then refuses exactly as it always has. Nothing here is ever
	// fetched by this package - the reads happen in internal/live/dataread,
	// and a stale or invented value on this path would become a live
	// ownership marker, which is why the phase re-reads on every run and no
	// cache exists.
	DataResults map[string]cty.Value
}

// ResolveWith is [Resolve] told everything the caller knows that the
// configuration does not carry. See [Context].
func ResolveWith(ctx context.Context, cfg *configs.Config, rctx Context) (*Result, tfdiags.Diagnostics) {
	return resolveWith(ctx, cfg, rctx)
}

// ResolveIn is [Resolve] told which cloud the run is against, so that the
// types whose import identity embeds the account or the region can be
// computed rather than deferred. See [CloudContext] for what those values
// are and where a caller gets them.
//
// A caller that does not have them passes the zero value, which is exactly
// what [Resolve] does: every type needing a value the context does not carry
// classifies [ClassNeedsDiscovery], naming the missing property, and marker
// discovery finds it instead. That fallback is the reason this parameter can
// exist at all without making the package need a provider - nothing here
// ever fetches a cloud property, and nothing here fails for want of one.
func ResolveIn(ctx context.Context, cfg *configs.Config, cloud CloudContext) (*Result, tfdiags.Diagnostics) {
	return resolveWith(ctx, cfg, Context{Cloud: cloud})
}

func resolveWith(ctx context.Context, cfg *configs.Config, rctx Context) (*Result, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if cfg == nil || cfg.Module == nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "No configuration to resolve",
			Detail:   "Identity resolution was given an empty configuration.",
		})
		return newResult(), diags
	}
	if cfg.Module.StaticEvaluator == nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Configuration loaded without a static evaluator",
			Detail:   "Identity resolution evaluates configuration expressions through the module's static evaluator, which this configuration does not have. Load the configuration with configs.Parser.LoadConfigDir or the configload package.",
		})
		return newResult(), diags
	}

	r := newResolver(ctx, cfg, rctx)

	result := newResult()
	// Collected for the whole configuration tree before anything is
	// classified, and for every type rather than only the admitted ones.
	// Two reasons: the config-side naming signal is what says which types
	// could join the table, so a type the table has never heard of is the
	// interesting case (signal.go); and the fallback that lets such a type
	// resolve at all consults the signal for its verdict, which has to be
	// the whole configuration's answer rather than however much of it the
	// walk happens to have reached. See [SynthesizeTypeIdentity].
	result.signal = r.collectSignal(cfg)
	r.signal = result.signal
	r.walkModule(cfg, addrs.RootModuleInstance, result)

	r.checkCollisions(result)
	r.warnUnsweepableTypes()

	return result, r.diags
}

// warnUnsweepableTypes says once per run which resource types this
// configuration relies on that no estate-wide sweep can recover.
//
// GitHub issue #107. A type absent from [DefaultTable] can still be admitted,
// when the provider's identity schema or the configuration's own arguments
// settle it ([SynthesizeTypeIdentity]) - and that admission is deliberately
// additive, so it only ever widens what plans. The sweep did not follow:
// internal/live/discovery draws its universe from [AdmittedTypes], which is
// the table's keys, so removing the last block of a synthesized type leaves
// the live resource in the account with no run that will ever propose
// removing it.
//
// The estate-wide tag sweep can see such a resource and reports it
// (discovery's ProblemUnsweepableOwnedType), but it is capability-gated and
// no command sets it today. This warning is on the path every run takes, and
// it fires while the block is still declared - which is the only moment the
// information is useful, because once the block is gone there is nothing
// left to warn about.
//
// A warning rather than an error: the type plans and applies correctly, and
// refusing it would withdraw coverage this fork went out of its way to add.
func (r *resolver) warnUnsweepableTypes() {
	names := make([]string, 0, len(r.synth))
	for typeName, entry := range r.synth {
		if entry != nil {
			names = append(names, typeName)
		}
	}
	sort.Strings(names)

	for _, typeName := range names {
		r.diags = r.diags.Append(tfdiags.Sourceless(
			tfdiags.Warning,
			SummaryNoOrphanRecovery,
			fmt.Sprintf(
				"%s is admitted by the provider's own identity schema rather than by this fork's admission table, so it plans and applies normally - but the estate-wide sweep draws its type universe from that table and will not list it. Deleting the last %s block from this configuration leaves the live resource in the account with no run proposing to remove it, and no warning at that point either. Remove it by hand when you remove the block. See live/LIMITATIONS.md, %q.",
				typeName, typeName, SummaryNoOrphanRecovery),
		))
	}
}

// walkModule classifies every managed resource instance in one node of the
// static module tree, at the given module instance, then recurses into its
// children in name order.
//
// modInst is the instance this call of cfg belongs to, chosen by the
// caller: [addrs.RootModuleInstance] for the root, or - for a for_each
// child - one [addrs.ModuleInstance.Child] call per key the enclosing
// module's for_each expands to (see the loop below). cfg.Path can no longer
// answer this by itself once a for_each module is in the tree, because
// cfg.Children holds one node per module CALL, not per instance: two
// instances of a for_each'd module share the same *configs.Config and are
// told apart only by the modInst each walkModule call carries.
//
// Recursion is depth-first and each call re-enters its own module before
// touching anything, so a resource reference resolved partway through one
// module's instances never observes another module's [resolver.mod] -
// see [resolver.enterModuleAt].
func (r *resolver) walkModule(cfg *configs.Config, modInst addrs.ModuleInstance, result *Result) {
	r.enterModuleAt(cfg, modInst)
	for _, rc := range sortedResources(cfg.Module.ManagedResources) {
		exp, ok := r.expansionFor(rc)
		if !ok {
			continue
		}
		for _, key := range exp.keys {
			addr := rc.Addr().Instance(key).Absolute(r.modInst)
			res, ok := r.instance(addr, rc.DeclRange)
			if !ok {
				continue
			}
			result.add(res)
		}
	}
	for _, name := range SortedChildNames(cfg.Children) {
		// Restored before every sibling, not just once before the loop: the
		// recursive r.walkModule call below, for whichever sibling sorts
		// before this one, ends by leaving r.mod/r.curCfg/r.modInst/r.eval
		// pointing at wherever ITS OWN deepest descendant left them -
		// enterModuleAt has no notion of a caller to return to. Without
		// this, a later sibling's own r.mod.ModuleCalls[name] lookup below
		// silently reads an unrelated module's call table (typically empty,
		// so its own count/for_each is missed entirely) whenever an
		// earlier-sorted sibling has any children of its own to recurse
		// into first. Found via govuk-infrastructure's opensearch
		// blue/green module: "blue_domain" sorts and is walked before
		// "snapshot_bucket", and blue_domain's own subtree left r.mod
		// pointing three levels down by the time snapshot_bucket's count
		// was read.
		r.enterModuleAt(cfg, modInst)
		child := cfg.Children[name]
		var forEach, count hcl.Expression
		if call, ok := r.mod.ModuleCalls[name]; ok && call != nil {
			forEach = call.ForEach
			count = call.Count
		}
		// count and for_each are mutually exclusive on a module call, the
		// same as on a resource; count is tried first only because that is
		// the order [resolver.buildExpansion] already uses for a resource's
		// own count/for_each, not because either can be set alongside the
		// other.
		var keys []addrs.InstanceKey
		var diag *hcl.Diagnostic
		if count != nil {
			keys, diag = ChildModuleCountKeys(r.ctx, r.mod, childSubject(name), count)
		} else {
			keys, diag = ChildModuleKeys(r.ctx, r.mod, childSubject(name), forEach)
		}
		if diag != nil {
			r.diags = r.diags.Append(diag)
			continue
		}
		for _, key := range keys {
			r.walkModule(child, modInst.Child(name, key), result)
		}
	}
}

// childSubject names a module call for a diagnostic about its own for_each
// expression: "module \"wrapped\"", matching how a resource's own for_each
// diagnostics name the resource.
func childSubject(name string) string {
	return fmt.Sprintf("module %q", name)
}

// checkCollisions reports two instances of the same type that resolve to
// the same identity. They would bind to one live resource, so the plan
// would treat one cloud object as two managed resources, the same class of
// ambiguity the marker path treats as a named error rather than picking a
// winner.
func (r *resolver) checkCollisions(result *Result) {
	// Grouped by Type+ident+base only - [cloudScopeKey.base] is the part
	// two resources must match exactly, the same partition the old
	// single-string key made when region played no part. Region is
	// resolved WITHIN a group instead (regionsDistinguish), because unlike
	// base it must never rule out a collision on its own: a group can hold
	// members with a known region, an unknown one, or both, and a member
	// with no known region has to be compared against every other member
	// of its own group, not just the first one seen (#217's own safety
	// direction - see regionsDistinguish's doc comment).
	seen := make(map[string][]Resolution)
	for _, res := range result.All() {
		var ident string
		switch res.Class {
		case ClassConcrete:
			ident = res.ImportID
		case ClassParentDerived:
			ident = res.Formula.String()
		default:
			continue
		}
		key := res.Type() + "\x00" + ident + "\x00" + res.cloudScope.base

		var collidesWith *Resolution
		for i := range seen[key] {
			if regionsDistinguish(seen[key][i].cloudScope, res.cloudScope) {
				continue
			}
			collidesWith = &seen[key][i]
			break
		}
		seen[key] = append(seen[key], res)
		if collidesWith == nil {
			continue
		}

		rng := hcl.Range{}
		if r.enterModuleFor(res.Addr.Module) {
			if rc := r.mod.ResourceByAddr(res.Addr.Resource.Resource); rc != nil {
				rng = rc.DeclRange
			}
		}
		r.errorf(rng, "Two resources with the same identity",
			"%s and %s both resolve to the identity %q. Both would bind to the same live resource, so one of them has to change: an identity is what tells a live-markers run which cloud object a configuration block owns.",
			collidesWith.Addr.String(), res.Addr.String(), ident)
	}
}

// regionsDistinguish is the only way two [cloudScopeKey] values sharing the
// same base can be told apart (#217): both sides must have determined an
// effective region, AND those regions must differ. Any other combination -
// either side's regionKnown false, or both known and equal - collides.
// This is deliberately asymmetric with how base is compared: base rules a
// pair IN or OUT on its own, region only ever rules a pair OUT, and only
// when this run is actually confident it knows both resources' regions and
// that they differ. A region this run could not determine is not evidence
// of "somewhere else" - it is exactly the same "cannot disambiguate, so
// don't" this package already applies to a `region` argument that fails to
// evaluate statically (see [resolver.resourceCloudScope]'s own doc
// comment), extended so the same caution holds when it is the OTHER side of
// a comparison that could not be determined.
func regionsDistinguish(a, b cloudScopeKey) bool {
	return a.regionKnown && b.regionKnown && a.region != b.region
}

func newResolver(ctx context.Context, cfg *configs.Config, rctx Context) *resolver {
	dataIndex, badResults := buildDataResultsIndex(rctx.DataResults)
	r := &resolver{
		ctx:        ctx,
		rootCfg:    cfg,
		cloud:      rctx.Cloud,
		schemas:    rctx.Schemas,
		dataIndex:  dataIndex,
		expansions: make(map[string]*expansion),
		expFailed:  make(map[string]bool),
		expVisit:   make(map[string]bool),
		insts:      make(map[string]Resolution),
		instFailed: make(map[string]bool),
		instVisit:  make(map[string]bool),
		synth:      make(map[string]*TypeIdentity),
	}
	for _, key := range badResults {
		// A result the index could not use is the calling code's defect, not
		// the configuration's, and it must not vanish: dropped silently it
		// would resurface as the generic dynamic-value refusal pointing the
		// user at their own file.
		r.diags = r.diags.Append(tfdiags.Sourceless(
			tfdiags.Error,
			SummaryUnusableDataResult,
			fmt.Sprintf(
				"The data-read phase handed resolution a result under %q, which is not usable: it must be an absolute data resource instance address, and one resource's instances must share one key kind with no gaps. This is a defect in the calling code, not in the configuration.",
				key),
		))
	}
	r.enterModule(cfg)
	return r
}

// SummaryUnusableDataResult is the caller-error refusal for a
// [Context.DataResults] entry resolution cannot index. See [newResolver].
const SummaryUnusableDataResult = "Unusable data-source result"

type resolver struct {
	ctx context.Context

	// rootCfg is the configuration [Resolve] was given, kept so that a
	// parent-derived reference or a resolved instance address can look up
	// any node of the static module tree by its module path. See
	// [resolver.enterModuleFor].
	rootCfg *configs.Config

	// mod, curCfg, modInst and eval are the module currently being worked
	// on: the module whose resources [resolver.expansionFor] and
	// [resolver.resolveInstance] are reading. They are mutated by
	// [resolver.enterModule] as the walk moves between modules, so nothing
	// in this package may cache them across a call that might change
	// modules. curCfg is mod's own *configs.Config node, kept alongside it
	// only because [providerscope.Resolve] needs the Config (for its
	// Parent/Path module-tree walk) rather than the bare Module - see
	// [resolver.resourceCloudScope].
	mod     *configs.Module
	curCfg  *configs.Config
	modInst addrs.ModuleInstance
	eval    *configs.StaticEvaluator

	cloud CloudContext
	diags tfdiags.Diagnostics

	// schemas are the provider's resource type schemas when the caller had
	// them, and nil when it did not. See [Context.Schemas].
	schemas map[string]providers.Schema

	// dataIndex is [Context.DataResults] regrouped per module instance and
	// resource, and nil when the caller passed none. See
	// [buildDataResultsIndex].
	dataIndex dataResultsIndex

	// signal is the whole configuration's naming signal, collected before
	// classification starts because the schema fallback's verdict depends on
	// it. Nil for the signal-only walk of [ScanConfig], which classifies
	// nothing.
	signal *ConfigSignal

	// synth memoizes the schema fallback per type, including its refusals: a
	// nil entry means "asked, and the schemas do not describe this type well
	// enough". See [SynthesizeTypeIdentity].
	synth map[string]*TypeIdentity

	// Expansion memo, keyed by the module instance and the resource address
	// (no instance key) - two resource blocks with the same local address in
	// different modules must not share an entry.
	expansions map[string]*expansion
	expFailed  map[string]bool
	expVisit   map[string]bool

	// Instance resolution memo, keyed by absolute instance address, which is
	// already module-qualified.
	insts      map[string]Resolution
	instFailed map[string]bool
	instVisit  map[string]bool

	// curInstanceAddr is the resource instance whose identity
	// resolveInstance is currently building, in
	// [addrs.AbsResourceInstance.String] form - empty outside any
	// resolveInstance call. Every diagnostic raised while it is set is
	// tagged with it as an [InstanceFailure] (see [resolver.errorf] and
	// [resolver.appendDiags]), so a caller can tell which instance a
	// diagnostic belongs to without parsing its rendered text. Set on entry
	// to resolveInstance and restored on exit (see resolveInstance's own
	// defer), the same save/restore discipline [resolver.enterModuleAt]
	// already uses for r.mod/r.modInst - required because resolveInstance
	// recurses into another resolveInstance call for every parent a
	// component references ([resolver.parentPart] -> [resolver.instance]),
	// and a diagnostic raised inside that nested call belongs to the
	// parent, not to whichever instance is waiting on it. See #221.
	curInstanceAddr string
}

// enterModule points the resolver at one node of the static module tree,
// with its self instance ([ModuleInstance]) as the module instance: the
// unkeyed reading that is lossless for the root and for a static module
// call, and the entry point for every caller that has not been told a more
// specific instance to use. See [resolver.enterModuleAt].
func (r *resolver) enterModule(cfg *configs.Config) {
	r.enterModuleAt(cfg, ModuleInstance(cfg))
}

// enterModuleAt points the resolver at one node of the static module tree
// as one specific instance of it, setting the module, that instance path,
// and its own static evaluator. Every entry point that is about to read
// [resolver.mod] or [resolver.eval] calls this (or [resolver.enterModule])
// first, rather than trusting whatever a previous call left behind.
//
// modInst is taken as given, never recomputed from cfg.Path: cfg.Path names
// the module CALL, and a for_each'd call's several instances all share one
// *configs.Config node, so cfg.Path alone cannot tell two of them apart (59c,
// keyed for_each on module blocks). [resolver.walkModule] is what supplies a
// keyed instance; every other caller passes the same instance
// [ModuleInstance] would compute, through [resolver.enterModule].
func (r *resolver) enterModuleAt(cfg *configs.Config, modInst addrs.ModuleInstance) {
	r.mod = cfg.Module
	r.curCfg = cfg
	r.modInst = modInst
	// Pure on purpose: an identity is a claim about which cloud object a
	// block owns, and a function that answers differently every time it is
	// called cannot make that claim. See impure.go.
	r.eval = cfg.Module.StaticEvaluator.Pure()
	// Data-read results, when the caller performed the pre-resolution read
	// phase: the evaluator answers exactly the data references the phase
	// covered in this module instance, and refuses everything else exactly
	// as before. See [Context.DataResults].
	if lookup := r.dataLookupFor(modInst); lookup != nil {
		r.eval = r.eval.WithDataResults(lookup)
	}
}

// enterModuleFor is [resolver.enterModuleAt] by module instance path, for
// the call sites - resolving a reference's parent, most of all - that have
// an address rather than a *configs.Config in hand. It reports false when
// the path names no module in this configuration's tree.
//
// The instance it enters is modInst exactly as given, keys included:
// [ConfigForModule] ignores keys on the way down to find the right
// *configs.Config (one node per call, not per instance, same reason
// [resolver.enterModuleAt] gives), but the resolver's own idea of "which
// instance is this" must not lose them, or two different instances of one
// for_each'd module would share an expansion and instance memo key (see
// [resolver.expKey]) and each would silently return the other's answer.
func (r *resolver) enterModuleFor(modInst addrs.ModuleInstance) bool {
	cfg, ok := ConfigForModule(r.rootCfg, modInst)
	if !ok || cfg.Module == nil {
		return false
	}
	r.enterModuleAt(cfg, modInst)
	return true
}

// expKey builds the expansion memo's key: the module instance a resource
// block belongs to, plus its own address within that module.
func (r *resolver) expKey(rc *configs.Resource) string {
	return r.modInst.String() + "\x00" + rc.Addr().String()
}

// lookupType is [LookupType] with the schema fallback behind it: a type the
// hand table does not cover still resolves when the provider's own identity
// schema describes it completely and the caller supplied the schemas. A
// caller that supplied none gets [LookupType] exactly.
func (r *resolver) lookupType(typeName string) (TypeIdentity, bool) {
	if entry, ok := LookupType(typeName); ok {
		return entry, true
	}
	if memo, asked := r.synth[typeName]; asked {
		if memo == nil {
			return TypeIdentity{}, false
		}
		return *memo, true
	}

	entry, ok := SynthesizeTypeIdentity(typeName, r.schemas, r.signal)
	if !ok {
		r.synth[typeName] = nil
		return TypeIdentity{}, false
	}
	r.synth[typeName] = &entry
	return entry, true
}

// instance resolves one managed resource instance, memoizing the result.
// rng is the source range to blame for the request: the resource's own
// declaration when called from the top-level walk, or the referencing
// expression when called for a parent.
func (r *resolver) instance(addr addrs.AbsResourceInstance, rng hcl.Range) (Resolution, bool) {
	key := addr.String()
	if res, ok := r.insts[key]; ok {
		return res, true
	}
	if r.instFailed[key] {
		return Resolution{}, false
	}
	if r.instVisit[key] {
		r.errorf(rng, "Circular identity reference",
			"The identity of %s depends on itself, directly or through other resources. Identity cannot be resolved for a cycle.", key)
		r.instFailed[key] = true
		return Resolution{}, false
	}
	r.instVisit[key] = true
	defer delete(r.instVisit, key)

	res, ok := r.resolveInstance(addr, rng)
	if !ok {
		r.instFailed[key] = true
		return Resolution{}, false
	}
	r.insts[key] = res
	return res, true
}

func (r *resolver) resolveInstance(addr addrs.AbsResourceInstance, rng hcl.Range) (Resolution, bool) {
	resAddr := addr.Resource.Resource

	// Every diagnostic raised from here down - directly, or through a
	// nested r.instance call resolving a DIFFERENT instance a component
	// references - gets tagged with whichever instance owns the call frame
	// at the moment it fires (see [resolver.curInstanceAddr],
	// [resolver.errorf], [resolver.appendDiags]). Restored on return so a
	// diagnostic raised after this call unwinds - by whichever instance
	// called it, or by nothing at all - is not misattributed to addr. See
	// #221.
	prevInstanceAddr := r.curInstanceAddr
	r.curInstanceAddr = addr.String()
	defer func() { r.curInstanceAddr = prevInstanceAddr }()

	if !r.enterModuleFor(addr.Module) {
		r.errorf(rng, "Reference to a module instance that does not exist",
			"%s is in %s, which is not part of this configuration's static module tree, so its identity cannot be resolved.",
			addr.String(), addr.Module.String())
		return Resolution{}, false
	}

	rc := r.mod.ResourceByAddr(resAddr)
	if rc == nil {
		r.errorf(rng, "Reference to undeclared resource",
			"%s is not declared in this configuration, so its identity cannot be resolved.", resAddr.String())
		return Resolution{}, false
	}

	// The referenced instance key has to be one this resource actually
	// expands to; otherwise a reference like aws_subnet.this.id (whole
	// resource, no key) would silently resolve against a nonexistent
	// NoKey instance.
	exp, ok := r.expansionFor(rc)
	if !ok {
		return Resolution{}, false
	}
	if !exp.hasKey(addr.Resource.Key) {
		r.errorf(rng, "Reference to a resource instance that does not exist",
			"%s does not exist. %s", addr.String(), exp.describe(resAddr))
		return Resolution{}, false
	}

	entry, ok := r.lookupType(resAddr.Type)
	if !ok {
		// This used to interpolate strings.Join(AdmittedTypes(), ", "),
		// which was reasonable when the table held a few dozen rows and
		// renders a 25KB diagnostic at the table's 800-plus rows. It also cited
		// "the roadmap", a document that is not in this repository - the
		// only text carrying that phrase is live/LIMITATIONS.md quoting it.
		//
		// This is lint.go's unadmitted-type refusal seen from the
		// resolution layer, and the two are meant to read as one rule, so
		// the wording tracks it. Found by the #101 message audit.
		r.errorf(rng, "Resource type outside the live-markers subset",
			"There is no identity knowledge for resource type %q, so %s cannot be admitted to a live-markers projection. "+
				"A type participates only if its identity is recoverable from the live system with no memory. "+
				"The table is generated from ratified identity rows and is not extensible from here; "+
				"see live/LIMITATIONS.md, \"unadmitted-type\".%s",
			resAddr.Type, addr.String(), r.schemaRefusal(resAddr.Type))
		return Resolution{}, false
	}

	if entry.ServerAssigned {
		return Resolution{
			Addr:   addr,
			Class:  ClassNeedsDiscovery,
			Reason: entry.Reason,
		}, true
	}

	if entry.RecordBacked {
		// No cloud identity to build, ever - the whole point of this class.
		// See [ClassRecordBacked] and [TypeIdentity.RecordBacked]. A
		// resolution this shallow is safe here only because a
		// RECORD_ADMITTED type never reaches resolution at all unless a
		// live block's record_store is configured (internal/live/lint's
		// checkManagedResources), which is the caller's gate, not this
		// package's.
		return Resolution{
			Addr:  addr,
			Class: ClassRecordBacked,
		}, true
	}

	// Cloud properties are checked before anything is read from the resource
	// body, so that a type this run cannot name gets the one honest answer -
	// "the account is not known here" - rather than an error about some
	// argument that would have been fine had the account been known.
	if missing, ok := r.missingCloudValue(entry); ok {
		return Resolution{
			Addr:   addr,
			Class:  ClassNeedsDiscovery,
			Reason: cloudReason(entry, missing),
		}, true
	}

	attrs, ok := r.identityArgs(rc, entry)
	if !ok {
		return Resolution{}, false
	}

	scope := exp.scope(addr.Resource.Key)
	var parts []Part
	// The same pieces again, split by the identity attribute each component
	// supplies. It is what makes an import ask the provider for
	// {"role": …, "policy_arn": …} rather than for the two joined by a "/"
	// that only this table knows about. Attributes are kept in component
	// order so that a rendered formula reads the way the import syntax does.
	byAttr := make(map[string][]Part)
	var attrOrder []string
	addTo := func(name string, got []Part) {
		if name == "" {
			return
		}
		if _, seen := byAttr[name]; !seen {
			attrOrder = append(attrOrder, name)
		}
		byAttr[name] = append(byAttr[name], got...)
	}

	// failed tracks whether any real component below could not be resolved.
	// The loop no longer returns on the first one (GitHub issue #221): every
	// component is still evaluated even after one fails, so a second,
	// independently-broken component raises its own diagnostic instead of
	// staying invisible behind the first. internal/live/check's cascade
	// fixpoint reclassifies a dependent as data-read-eligible only when
	// EVERY diagnostic tagged with this instance's address traces back to
	// one - which requires every failing component to have actually been
	// reached and diagnosed, not just the first.
	failed := false
	for _, comp := range entry.Components {
		if comp.Cloud != CloudNone {
			// Present: missingCloudValue already refused the alternative.
			v, _ := r.cloud.value(comp.Cloud)
			got := []Part{{Literal: v}}
			parts = append(parts, got...)
			addTo(comp.identityAttrFor(""), got)
			continue
		}
		if len(comp.Attrs) == 0 {
			got := []Part{{Literal: comp.Literal}}
			parts = append(parts, got...)
			addTo(comp.identityAttrFor(""), got)
			continue
		}
		attr := firstPresent(attrs, comp.Attrs)
		if attr == nil {
			if comp.Default != "" {
				// The provider documents what omission means for this
				// argument (an omitted event_bus_name is the "default"
				// bus), so the identity is computable without it - see
				// [Component.Default].
				got := []Part{{Literal: comp.Default}}
				parts = append(parts, got...)
				addTo(comp.identityAttrFor(comp.Attrs[0]), got)
				continue
			}
			if comp.ServerAssignedIfAbsent {
				// The provider's own Argument Reference documents this
				// argument as one it fills in itself when the
				// configuration leaves it blank ("If omitted, Terraform
				// will assign a random, unique name" and its siblings -
				// see [Component.ServerAssignedIfAbsent] and #190). That
				// is the identical situation the *_prefix branch just
				// below handles for a different spelling of the same
				// convention: not a missing argument, but a name this run
				// cannot compute before the object exists.
				return Resolution{
					Addr:  addr,
					Class: ClassNeedsDiscovery,
					Reason: fmt.Sprintf(
						"%s has no value for %s; the provider assigns one automatically when it is omitted, so the value is not known until the object exists.",
						addr.String(), orList(comp.Attrs)),
				}, true
			}
			if prefixAttr, base := firstPrefixSibling(attrs, comp.Attrs); prefixAttr != nil {
				// The block names the object through "<base>_prefix", not
				// "<base>" - a convention the provider documents across
				// dozens of types (aws_db_parameter_group, aws_iam_role,
				// aws_s3_bucket, aws_cloudwatch_log_group, and more) to mean
				// "assign a random suffix to this prefix at create time".
				// That is not a missing argument, the way this error reads
				// for every other component: it is a name this run cannot
				// compute before the object exists, the identical situation
				// [TypeIdentity.ServerAssigned] already gives its own
				// resolution class to at the whole-type level. See #190.
				return Resolution{
					Addr:  addr,
					Class: ClassNeedsDiscovery,
					Reason: fmt.Sprintf(
						"%s is named through %s rather than %q; the provider appends a random suffix to the prefix at create time, so the resulting name is not known until the object exists.",
						addr.String(), prefixAttr.Name, base),
				}, true
			}
			r.errorf(rc.DeclRange, "Identity argument not set",
				"%s has no value for %s, so its import identity (%s) cannot be built.",
				addr.String(), orList(comp.Attrs), entry.ImportSyntax)
			failed = true
			continue
		}
		ident := r.identifier(addr, attr.Name, attr.Range)
		expr := attr.Expr
		if comp.SoleElement {
			narrowed, ok := r.soleElementExpr(expr, scope, attr, ident)
			if !ok {
				failed = true
				continue
			}
			expr = narrowed
		}
		got, ok := r.resolveExpr(expr, scope, ident)
		if !ok {
			failed = true
			continue
		}
		parts = append(parts, got...)
		addTo(comp.identityAttrFor(attr.Name), got)
	}
	if failed {
		return Resolution{}, false
	}

	res := classify(addr, coalesce(parts), attrFormulas(byAttr, attrOrder), entry.IdentityObjectOnly)
	res.cloudScope = r.resourceCloudScope(rc, scope)
	return res, true
}

// attrFormulas turns the per-attribute part lists into the ordered form a
// [Formula] carries, each one coalesced the way the whole import ID is.
func attrFormulas(byAttr map[string][]Part, order []string) []AttrFormula {
	if len(order) == 0 {
		return nil
	}
	out := make([]AttrFormula, 0, len(order))
	for _, name := range order {
		out = append(out, AttrFormula{Name: name, Parts: coalesce(byAttr[name])})
	}
	return out
}

// classify turns a resolved part list into a Resolution: concrete if every
// part is a literal, parent-derived if any part waits on a live value. The
// per-attribute split follows the same fork, since it is the same parts.
// idObjectOnly is [TypeIdentity.IdentityObjectOnly]: the type's identity has
// several attributes and no separator to join them with, so there is no
// import-ID string to build and the concatenation below must not run. See
// that field's doc comment for what the concatenation would otherwise hand
// out.
func classify(addr addrs.AbsResourceInstance, parts []Part, attrs []AttrFormula, idObjectOnly bool) Resolution {
	var parents []addrs.AbsResourceInstance
	seen := make(map[string]bool)
	for _, p := range parts {
		if p.Parent == nil {
			continue
		}
		k := p.Parent.Instance.String()
		if !seen[k] {
			seen[k] = true
			parents = append(parents, p.Parent.Instance)
		}
	}

	if len(parents) == 0 {
		var buf strings.Builder
		for _, p := range parts {
			buf.WriteString(p.Literal)
		}
		var values map[string]string
		if len(attrs) > 0 {
			values = make(map[string]string, len(attrs))
			for _, a := range attrs {
				var v strings.Builder
				for _, p := range a.Parts {
					v.WriteString(p.Literal)
				}
				values[a.Name] = v.String()
			}
		}
		importID := buf.String()
		if idObjectOnly {
			// The values are the whole answer, and joining them would
			// invent a grammar no schema carries. An empty ImportID is what
			// makes the projection import by identity object or fail
			// loudly, rather than fall back to a plausible-looking string.
			importID = ""
		}
		return Resolution{
			Addr:           addr,
			Class:          ClassConcrete,
			ImportID:       importID,
			IdentityValues: values,
		}
	}

	sort.Slice(parents, func(i, j int) bool {
		return parents[i].String() < parents[j].String()
	})
	return Resolution{
		Addr:  addr,
		Class: ClassParentDerived,
		Formula: &Formula{
			Parts:   parts,
			Attrs:   attrs,
			Parents: parents,
		},
	}
}

// missingCloudValue reports the first cloud property this entry's identity
// needs that the run was not given, in component order.
func (r *resolver) missingCloudValue(entry TypeIdentity) (CloudValue, bool) {
	for _, comp := range entry.Components {
		if comp.Cloud == CloudNone {
			continue
		}
		if _, ok := r.cloud.value(comp.Cloud); !ok {
			return comp.Cloud, true
		}
	}
	return CloudNone, false
}

// cloudReason is the needs-discovery explanation for an identity that embeds
// a property of the cloud rather than of the configuration. It has to read
// as an operator-facing sentence, because it is printed as one: the
// projection's omission section quotes it verbatim.
func cloudReason(entry TypeIdentity, missing CloudValue) string {
	return fmt.Sprintf(
		"an %s imports by %s, which embeds the %s, and that is a property of the cloud this run is pointed at rather than of the configuration; nothing has told this run what it is, so the identity cannot be built here.",
		entry.Type, entry.ImportSyntax, missing.describe(),
	)
}

// identityArgs pulls just the arguments the type's identity needs out of
// the resource body. Everything else in the body, including nested blocks,
// is ignored: identity resolution has no business decoding a whole
// resource.
func (r *resolver) identityArgs(rc *configs.Resource, entry TypeIdentity) (hcl.Attributes, bool) {
	var names []string
	seen := make(map[string]bool)
	add := func(n string) {
		if !seen[n] {
			seen[n] = true
			names = append(names, n)
		}
	}
	for _, comp := range entry.Components {
		for _, n := range comp.Attrs {
			add(n)
			// Every provider that names an object through one of these
			// arguments also offers the "<name>_prefix" sibling documented
			// to make the provider assign a random suffix at create time
			// (aws_db_parameter_group's name/name_prefix, aws_iam_role's
			// name/name_prefix, and so on across the provider - #190). It is
			// never this component's own identity value, so it is pulled
			// only to let [resolver.resolveInstance] tell "nothing named
			// this object" apart from "named it in a way that is not known
			// until the object exists" - the same distinction
			// [TypeIdentity.ServerAssigned] already draws at the whole-type
			// level, drawn here per instance instead.
			if !strings.HasSuffix(n, "_prefix") {
				add(n + "_prefix")
			}
		}
	}
	sort.Strings(names)

	schema := &hcl.BodySchema{}
	for _, n := range names {
		schema.Attributes = append(schema.Attributes, hcl.AttributeSchema{Name: n})
	}

	content, _, diags := rc.Config.PartialContent(schema)
	if diags.HasErrors() {
		r.appendDiags(diags)
		return nil, false
	}
	return content.Attributes, true
}

// resolveExpr turns one argument expression into import-ID parts.
// soleElementExpr is [Component.SoleElement]'s whole implementation: given
// the expression a list/set-typed identity argument was written with, it
// returns the one sub-expression to resolve in its place, or refuses.
//
// hcl.ExprList succeeds only for a syntactic list/set/tuple CONSTRUCT
// ("[...]"), so a Component.Attrs list mixing a collection-typed name with a
// genuinely scalar one (aws_security_group_rule pairs
// cidr_blocks/ipv6_cidr_blocks/prefix_list_ids, all lists, with
// source_security_group_id, a plain string) narrows only the members that
// need it: it fails harmlessly on the scalar member, which falls through to
// [resolver.soleElementFromValue] and then, if that finds no collection
// either, returns unchanged to resolve as a plain scalar.
//
// soleElementFromValue is the fallback for the construct not being
// syntactic: a variable or local typed list(string)/set(string) - a
// *_cidr_blocks argument declared that way is the common shape - is exactly
// as "written in configuration, not merely producing one at apply time" as
// every other identity component in this package already requires; nothing
// about the one-element rule is specific to how the collection was spelled.
func (r *resolver) soleElementExpr(expr hcl.Expression, scope instScope, attr *hcl.Attribute, ident configs.StaticIdentifier) (hcl.Expression, bool) {
	elems, diags := hcl.ExprList(expr)
	if diags.HasErrors() || elems == nil {
		if narrowed, ok, applicable := r.soleElementFromValue(expr, scope, attr, ident); applicable {
			return narrowed, ok
		}
		return expr, true
	}
	if len(elems) != 1 {
		r.errorf(attr.Range, "Ambiguous list-valued identity argument",
			"%s has %d elements. This component's identity can only be built when %s carries exactly one value; the AWS API - not this configuration's list order - decides how more than one composes into the real object, so this package will not guess which one to use.",
			ident.Subject, len(elems), attr.Name)
		return nil, false
	}
	return elems[0], true
}

// soleElementFromValue is [resolver.soleElementExpr]'s fallback when expr is
// not a syntactic list construct: it evaluates expr the same way
// [resolver.resolveExpr] eventually would, and applies the one-element rule
// structurally when - and only when - the result is itself a known
// list/set/tuple, rather than letting the whole collection reach
// [resolver.stringValue] and refuse as "Non-string identity argument".
//
// applicable is false whenever nothing here applies: expr references a
// managed resource (isSymbolic, nothing evaluable without the cloud), or
// evaluation fails for a reason unrelated to being a collection, or it
// succeeds but is not one. In every applicable=false case, no diagnostic is
// left behind - the caller's own unchanged-expression path stands, and
// resolveExpr raises whatever it would have raised for the original
// expression. When applicable is true, ok carries the same "exactly one
// element" verdict the syntactic case enforces, and the diagnostic (if any)
// is already recorded.
func (r *resolver) soleElementFromValue(expr hcl.Expression, scope instScope, attr *hcl.Attribute, ident configs.StaticIdentifier) (hcl.Expression, bool, bool) {
	if r.isSymbolic(expr, scope) {
		return nil, false, false
	}
	mark := len(r.diags)
	val, ok := r.evalStatic(expr, scope, ident)
	if !ok {
		r.diags = r.diags[:mark]
		return nil, false, false
	}
	ty := val.Type()
	if !ty.IsListType() && !ty.IsSetType() && !ty.IsTupleType() {
		return nil, false, false
	}
	if val.IsNull() || !val.IsWhollyKnown() {
		return nil, false, false
	}
	n := 0
	var only cty.Value
	for it := val.ElementIterator(); it.Next(); {
		_, v := it.Element()
		only = v
		n++
	}
	if n != 1 {
		r.errorf(attr.Range, "Ambiguous list-valued identity argument",
			"%s has %d elements. This component's identity can only be built when %s carries exactly one value; the AWS API - not this configuration's list order - decides how more than one composes into the real object, so this package will not guess which one to use.",
			ident.Subject, n, attr.Name)
		return nil, false, true
	}
	return &hclsyntax.LiteralValueExpr{Val: only, SrcRange: attr.Range}, true, true
}

func (r *resolver) resolveExpr(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	if !r.isSymbolic(expr, scope) {
		mark := len(r.diags)
		val, ok := r.evalStatic(expr, scope, ident)
		if ok {
			s, ok := r.stringValue(val, expr, ident)
			if !ok {
				return nil, false
			}
			return []Part{{Literal: s}}, true
		}

		// evalStatic just failed to evaluate the WHOLE expression, which,
		// for a reference into a local or a module variable, means it tried
		// to evaluate that local's or that variable's entire definition,
		// not only the part this expression actually selects. #178's
		// local-values fix: retry by decomposing that definition
		// structurally instead, the same way a direct resource reference
		// already resolves (resolveTraversal/parentPart), so a
		// resource-attribute reference reached only through a local's own
		// value resolves rather than refusing the whole block. See
		// localvalue.go. It changes nothing when the shape is not one this
		// package understands: the diagnostic evalStatic already recorded
		// stands, unreplaced.
		markAfterEval := len(r.diags)
		if parts, leafOK, applicable := r.namedLeaf(expr, scope, ident); applicable {
			r.diags = append(r.diags[:mark:mark], r.diags[markAfterEval:]...)
			return parts, leafOK
		}
		return nil, false
	}

	switch e := expr.(type) {
	case *hclsyntax.TemplateExpr:
		var parts []Part
		for _, sub := range e.Parts {
			got, ok := r.resolveExpr(sub, scope, ident)
			if !ok {
				return nil, false
			}
			parts = append(parts, got...)
		}
		return parts, true
	case *hclsyntax.TemplateWrapExpr:
		return r.resolveExpr(e.Wrapped, scope, ident)
	case *hclsyntax.ParenthesesExpr:
		return r.resolveExpr(e.Expression, scope, ident)
	}

	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() {
		if parts, ok, applicable := r.resolveIndexedTraversal(expr, scope, ident); applicable {
			return parts, ok
		}
		r.errorf(expr.Range(), "Identity not resolvable from configuration",
			"%s refers to another resource inside an expression that identity resolution cannot follow. "+
				"A resource reference contributes to an identity only as a whole reference or as an interpolation in a string template; "+
				"it cannot be passed through functions or operators, because the value it produces is not known until apply.",
			ident.Subject)
		return nil, false
	}
	return r.resolveTraversal(trav, scope, ident)
}

// resolveIndexedTraversal decomposes a reference into another resource
// selected by a computed index - aws_subnet.this[each.key].id or
// aws_instance.this[count.index].private_ip - which hcl.AbsTraversalForExpr
// cannot turn into a traversal because the index is an expression rather
// than a literal HCL parses directly into a traversal step. The index is
// evaluated the same way any other identity-argument expression is
// ([resolver.evalStatic]), against the same per-instance scope that already
// carries each.key/each.value or count.index ([expansion.scope]) - so this
// is not new evaluation machinery, only a second way to reach
// [resolver.parentPart] once the addressed instance is known, alongside the
// literal-index and bare-reference paths [resolver.resolveTraversal]
// already has via hcl.AbsTraversalForExpr.
//
// applicable is false whenever expr is not this shape at all: not a single
// attribute step following one index into what turns out to be a bare
// managed-resource reference. The caller's own "cannot follow" diagnostic
// stands unreplaced in that case. When applicable is true, ok reports
// whether resolution succeeded, and a diagnostic has already been recorded
// in its place when it did not - either by this function directly, or by
// whatever evaluated the index or the target's own expansion.
func (r *resolver) resolveIndexedTraversal(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (parts []Part, ok bool, applicable bool) {
	rel, isRel := expr.(*hclsyntax.RelativeTraversalExpr)
	if !isRel {
		return nil, false, false
	}
	idx, isIdx := rel.Source.(*hclsyntax.IndexExpr)
	if !isIdx {
		return nil, false, false
	}
	if len(rel.Traversal) != 1 {
		return nil, false, false
	}
	attrStep, isAttr := rel.Traversal[0].(hcl.TraverseAttr)
	if !isAttr {
		return nil, false, false
	}

	trav, diags := hcl.AbsTraversalForExpr(idx.Collection)
	if diags.HasErrors() {
		return nil, false, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		return nil, false, false
	}
	resAddr, isRes := ref.Subject.(addrs.Resource)
	if !isRes || len(ref.Remaining) > 0 || resAddr.Mode != addrs.ManagedResourceMode {
		return nil, false, false
	}

	rc := r.mod.ResourceByAddr(resAddr)
	if rc == nil {
		return nil, false, false
	}
	exp, expOK := r.expansionFor(rc)
	if !expOK {
		// The target's own expansion already failed and already carries a
		// diagnostic, wherever that first happened - see [resolveResourceRef]
		// for the same pattern.
		return nil, false, true
	}

	keyVal, keyOK := r.evalStatic(idx.Key, scope, ident)
	if !keyOK {
		// evalStatic already recorded why.
		return nil, false, true
	}
	key, keyIsValid := indexKeyValue(keyVal)
	if !keyIsValid {
		r.errorf(idx.Key.Range(), "Identity not resolvable from configuration",
			"%s indexes %s with a value that is not a string or a whole number, so it cannot select one of its instances.",
			ident.Subject, resAddr.String())
		return nil, false, true
	}
	if !exp.hasKey(key) {
		r.errorf(idx.SrcRange, "Reference to a resource instance that does not exist",
			"%s does not exist. %s", resAddr.Instance(key).String(), exp.describe(resAddr))
		return nil, false, true
	}

	got, gotOK := r.parentPart(resAddr.Instance(key).Absolute(r.modInst), attrStep.Name, expr.Range(), ident)
	return got, gotOK, true
}

// indexKeyValue turns an evaluated index expression into the instance key it
// names, the same two key kinds every resource expansion in this package
// already produces ([resolver.countExpansion], [resolver.forEachExpansion]).
func indexKeyValue(val cty.Value) (addrs.InstanceKey, bool) {
	if val.IsNull() || !val.IsKnown() || val.IsMarked() {
		return nil, false
	}
	switch {
	case val.Type() == cty.String:
		return addrs.StringKey(val.AsString()), true
	case val.Type() == cty.Number:
		var n int
		if err := gocty.FromCtyValue(val, &n); err != nil {
			return nil, false
		}
		return addrs.IntKey(n), true
	}
	return nil, false
}

// resolveTraversal turns a reference to another resource's attribute into a
// single part: a literal when that parent is already concrete, a parent
// reference otherwise.
func (r *resolver) resolveTraversal(trav hcl.Traversal, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	rng := trav.SourceRange()

	if trav.RootName() == "each" && scope.eachParent != nil {
		// each.value in a for_each-over-a-resource block is the parent
		// instance with the same key, so each.value.id is that parent's
		// id.
		if len(trav) != 3 || !isAttrStep(trav[1], "value") {
			r.errorf(rng, "Unsupported each.value reference",
				"Only each.value.<attribute> can contribute to an identity when for_each iterates over another resource, but %s was referenced by %s.",
				traversalString(trav), ident.Subject)
			return nil, false
		}
		attrStep, ok := trav[2].(hcl.TraverseAttr)
		if !ok {
			r.errorf(rng, "Unsupported each.value reference",
				"Only each.value.<attribute> can contribute to an identity when for_each iterates over another resource, but %s was referenced by %s.",
				traversalString(trav), ident.Subject)
			return nil, false
		}
		parent := scope.eachParent.Instance(scope.key).Absolute(r.modInst)
		return r.parentPart(parent, attrStep.Name, rng, ident)
	}

	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		r.appendDiags(refDiags)
		return nil, false
	}

	var instAddr addrs.ResourceInstance
	switch subject := ref.Subject.(type) {
	case addrs.Resource:
		instAddr = subject.Instance(addrs.NoKey)
	case addrs.ResourceInstance:
		instAddr = subject
	default:
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s refers to %s, which identity resolution cannot read. An identity can be composed from constants, variables, locals, path and terraform/tofu values, and other managed resources' identity attributes.",
			ident.Subject, ref.Subject.String())
		return nil, false
	}
	if instAddr.Resource.Mode != addrs.ManagedResourceMode {
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s refers to %s, an ephemeral resource. An ephemeral value exists only for the duration of one run, so it cannot name a cloud object that has to be findable on the next one.",
			ident.Subject, instAddr.String())
		return nil, false
	}
	if len(ref.Remaining) != 1 {
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s refers to %s, but an identity can only be built from a single attribute of another resource (its identity attribute).",
			ident.Subject, traversalString(trav))
		return nil, false
	}
	attrStep, ok := ref.Remaining[0].(hcl.TraverseAttr)
	if !ok {
		r.errorf(rng, "Identity not resolvable from configuration",
			"%s indexes into %s. An identity can only be built from a single attribute of another resource.",
			ident.Subject, instAddr.String())
		return nil, false
	}

	return r.parentPart(instAddr.Absolute(r.modInst), attrStep.Name, rng, ident)
}

func (r *resolver) parentPart(parent addrs.AbsResourceInstance, attrName string, rng hcl.Range, ident configs.StaticIdentifier) ([]Part, bool) {
	parentRes, ok := r.instance(parent, rng)
	if !ok {
		r.errorf(rng, "Unresolvable identity",
			"%s depends on the identity of %s, which could not be resolved (see the other error).",
			ident.Subject, parent.String())
		return nil, false
	}

	entry, _ := r.lookupType(parent.Resource.Resource.Type)

	// The parent's own resolution already answers this, one attribute at a
	// time, whenever the parent's entry says which component supplies which
	// identity attribute: that split is [Resolution.IdentityValues] for a
	// concrete parent and [Formula.Attrs] for a parent-derived one. Reading
	// it is not a new claim about the cloud - it is the same parts, from the
	// same arguments, that the parent's own identity is built from, so a
	// reference to one attribute can never disagree with the identity the
	// parent already asserts.
	//
	// It is tried before IdentityAttrs because it is the more precise of the
	// two, and because the fallback below is wrong wherever they differ. See
	// [Resolution.attrParts].
	if got, ok := parentRes.attrParts(attrName); ok {
		return got, true
	}

	if !entry.hasIdentityAttr(attrName) {
		// attrName is not part of parent's cloud identity at all - but it
		// may still be an ordinary argument parent's own block sets
		// literally, which is fully static and has nothing to do with
		// identity (GitHub issue #220): aws_route53_record.mx reading
		// aws_route53_zone.production.name, where the zone's identity is
		// zone_id and name is just a plain string the block wrote. See
		// [resolver.siblingLiteralExpr] for the boundary that makes this
		// safe.
		if expr, scope, applicable := r.siblingLiteralExpr(parent, attrName); applicable {
			return r.resolveExpr(expr, scope, ident)
		}

		detail := fmt.Sprintf(
			"%s reads %s.%s, but %q is not an identity attribute of %s. ",
			ident.Subject, parent.String(), attrName, attrName, parent.Resource.Resource.Type)
		if len(entry.IdentityAttrs) == 0 {
			detail += fmt.Sprintf("No attribute of %s carries its import identity, so nothing about it can be recovered without reading the cloud.", parent.Resource.Resource.Type)
		} else {
			detail += fmt.Sprintf("Only %s can be resolved without reading the cloud; every other attribute is known only after apply.", orList(entry.IdentityAttrs))
		}
		r.errorf(rng, "Not an identity attribute", "%s", detail)
		return nil, false
	}

	if parentRes.Class == ClassConcrete {
		// The parent's identity is already a string, so this stays
		// concrete rather than becoming a formula.
		return []Part{{Literal: parentRes.ImportID}}, true
	}
	return []Part{{Parent: &ParentRef{Instance: parent, Attr: attrName}}}, true
}

// siblingLiteralExpr returns the expression parent's own resource block
// gives attrName, and the per-instance scope to evaluate it in, when doing
// so is safe: GitHub issue #220. A sibling's cloud identity
// ([TypeIdentity.IdentityAttrs]) is one thing; an ordinary argument the
// block wrote itself is another, and nothing about the second kind depends
// on the cloud - the value sits in configuration exactly where a plain
// read of it would find one of this package's own identity components.
//
// The boundary is the provider schema's own Computed flag, not a name
// list: an attribute the schema marks Computed, with or without Optional
// alongside it, is one the provider can invent or override - S3's
// Optional+Computed "bucket" argument is the shape this refuses even
// though most callers set it themselves, because Computed is the schema's
// own admission that the stored value need not equal what the
// configuration wrote. Required, and Optional-without-Computed, are the
// only two flag combinations left, and both mean the provider has no path
// of its own to a different value - matching the same rule
// [SynthesizeTypeIdentity]'s locallyDefaultable already applies from the
// opposite direction, to decide what MAY be missing rather than what is
// safe to read.
//
// applicable is false whenever this rule does not apply at all: no
// schemas were supplied to this run, the sibling's type has none, attrName
// names no top-level attribute of it, that attribute is Computed, or the
// sibling's own block does not set it (an Optional-without-Computed
// argument left out is simply absent, not a value to resolve). The caller
// falls back to its own refusal, unchanged, in every such case.
func (r *resolver) siblingLiteralExpr(parent addrs.AbsResourceInstance, attrName string) (expr hcl.Expression, scope instScope, applicable bool) {
	if r.schemas == nil {
		return nil, instScope{}, false
	}
	typeSchema, hasSchema := r.schemas[parent.Resource.Resource.Type]
	if !hasSchema || typeSchema.Block == nil {
		return nil, instScope{}, false
	}
	attrSchema, known := typeSchema.Block.Attributes[attrName]
	if !known || attrSchema.Computed {
		return nil, instScope{}, false
	}

	if !r.enterModuleFor(parent.Module) {
		return nil, instScope{}, false
	}
	rc := r.mod.ResourceByAddr(parent.Resource.Resource)
	if rc == nil {
		return nil, instScope{}, false
	}
	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: attrName}},
	})
	if diags.HasErrors() {
		return nil, instScope{}, false
	}
	attr, set := content.Attributes[attrName]
	if !set {
		return nil, instScope{}, false
	}

	exp, expOK := r.expansionFor(rc)
	if !expOK || !exp.hasKey(parent.Resource.Key) {
		// The sibling's own expansion already failed, or (should not
		// happen: parent is an instance [resolver.instance] already
		// walked) does not carry this key - either way nothing here can
		// answer, and no new diagnostic belongs to this attribute read.
		return nil, instScope{}, false
	}
	return attr.Expr, exp.scope(parent.Resource.Key), true
}

// isSymbolic reports whether an expression references something whose value
// this package refuses to evaluate and instead handles structurally: a
// managed resource, or each.value when for_each iterates over a resource.
func (r *resolver) isSymbolic(expr hcl.Expression, scope instScope) bool {
	for _, trav := range expr.Variables() {
		switch trav.RootName() {
		case "each":
			if scope.eachParent != nil && len(trav) >= 2 && isAttrStep(trav[1], "value") {
				return true
			}
		case "count", "var", "local", "path", "terraform", "tofu", "module", "data", "self":
			// Not symbolic: either statically evaluable or a case
			// evalStatic will reject with its own message.
		default:
			if _, bound := scope.vars[trav.RootName()]; bound {
				// A for-comprehension's own loop variable (see
				// [resolver.evalPure]'s identical check), reached here
				// because a caller evaluates one piece of the
				// comprehension - a value clause referencing a sibling,
				// #220's own shape - outside the ForExpr node that would
				// otherwise scope it for hcl.Expression.Variables(). Not a
				// resource reference: evalPure already supplies it through
				// the child EvalContext this same scope builds.
				continue
			}
			// Anything else in a resource argument is a managed resource
			// reference; whether it is declared is checked later.
			return true
		}
	}
	return false
}

// evalStatic evaluates an expression that references no managed resources,
// through the module's static evaluator plus a child scope carrying
// each/count for the instance being resolved.
func (r *resolver) evalStatic(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (cty.Value, bool) {
	if names := impureCallsIn(expr); len(names) > 0 {
		r.errorf(expr.Range(), "Identity derived from an impure function",
			"%s calls %s, which returns a different value every time it is evaluated. "+
				"An identity is the answer to \"which live object does this block own\", and a value that changes between runs cannot answer it: "+
				"the first apply would create a resource under a name nothing can compute again, and every plan after it would propose creating another one. "+
				"Nothing detects that afterwards, because each run's fabricated identity looks like a perfectly ordinary one. "+
				"Pass the value in as a variable, or let the cloud assign the name and let the tofu-address marker record the ownership.",
			ident.Subject, orListQuoted(names))
		return cty.NilVal, false
	}

	val, diags := r.evalPure(expr, scope, ident)
	if diags.HasErrors() {
		r.appendDiags(diags)
		return cty.NilVal, false
	}
	return val, true
}

// evalPure is the evaluation itself, with its diagnostics handed back
// rather than recorded. [resolver.evalStatic] records them, because an
// argument that will not evaluate is a resolution failure; the config-side
// naming signal (signal.go) discards them, because an argument it cannot
// read is still an argument the configuration sets.
//
// The recover guards against a panic this package's own traversal filter
// cannot see coming: a var.* reference inside a module reached through a
// for_each'd ancestor call (59c, issue #59 phase 3) can resolve, several
// layers down inside [configs.StaticEvaluator]'s own variable-resolution
// machinery, to an expression that itself references the ancestor's own
// each.key or each.value. That resolution runs through
// cfg.Module.StaticEvaluator - built once by internal/configs when the
// module tree is loaded, entirely independent of [resolver.eval]'s own
// per-instance [configs.StaticEvaluator.WithRepetitionData] dup (see
// [instScope.repetition]) - so it never receives repetition data at all and
// panics ("Not Available in Static Context") rather than erroring. This
// package never evaluates such an expression on purpose (see
// [ChildModuleKeys]'s doc: a module call's own for_each is evaluated in its
// parent's scope, never a child's variables), but nothing stops a resource
// argument from referencing one anyway, and a crash here would take the
// whole run down over one identity component this package was always going
// to refuse. Degrading to a clean "cannot evaluate" is the same choice
// [lint.evalStatic] already makes for the class of panic it guards against.
//
// #213 closed the sibling gap this comment used to describe alongside this
// one: a local's own definition referencing each.value/each.key/count.index
// directly, reached from an identity-bearing expression in the SAME
// instance's own arguments. That case no longer reaches this recover at
// all - [instScope.repetition] carries the instance's repetition data on
// [resolver.eval] itself, so every nested [configs.StaticEvaluator] scope
// this instance's own evaluation builds (a local's own definition among
// them) sees the same each/count the instance's other arguments see, and a
// reference outside what is actually known refuses cleanly through
// [configs.StaticIdentifier]'s ordinary diagnostics instead of panicking.
func (r *resolver) evalPure(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) (val cty.Value, diags tfdiags.Diagnostics) {
	defer func() {
		if rec := recover(); rec != nil {
			val = cty.NilVal
			diags = tfdiags.Diagnostics{}.Append(&hcl.Diagnostic{
				Severity: hcl.DiagError,
				Summary:  "Expression not evaluable here",
				Detail: fmt.Sprintf(
					"%s could not be evaluated: %v. This is most often a reference that, several layers down, depends on a for_each key this package does not propagate across a module boundary (issue #59, 59c) - see live/LIMITATIONS.md, \"child-module\".",
					ident.Subject, rec),
				Subject: expr.Range().Ptr(),
			})
		}
	}()

	var travs []hcl.Traversal
	for _, trav := range expr.Variables() {
		if _, bound := scope.vars[trav.RootName()]; bound {
			// A for-comprehension's own loop variable, bound by
			// forEachOverComprehension below - never "each" or "count",
			// which are answered through [instScope.repetition] and
			// [configs.StaticEvaluator.WithRepetitionData] below instead,
			// at every depth a reference is resolved at, not only this top
			// level. This is a local binding the static evaluator has no
			// notion of, and would either panic on or misreport as an
			// undeclared reference.
			continue
		}
		travs = append(travs, trav)
	}

	refs, refDiags := lang.References(addrs.ParseRef, travs)
	if refDiags.HasErrors() {
		return cty.NilVal, diags.Append(refDiags)
	}

	// scope.repetition is exactly the each.key/each.value/count.index this
	// resource instance's own arguments already see (built once, in
	// [expansion.scope], from the same expansion that decided this
	// instance exists) - never re-derived here, so a local value reached
	// through this expression sees the identical values, not a
	// recomputation of them. See [configs.StaticEvaluator.WithRepetitionData].
	eval := r.eval.WithRepetitionData(scope.repetition)
	hclCtx, ctxDiags := eval.EvalContext(r.ctx, ident, refs)
	if ctxDiags.HasErrors() {
		return cty.NilVal, diags.Append(ctxDiags)
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}
	if len(scope.vars) > 0 {
		child := hclCtx.NewChild()
		child.Variables = scope.vars
		hclCtx = child
	}

	val, valDiags := expr.Value(hclCtx)
	if valDiags.HasErrors() {
		return cty.NilVal, diags.Append(valDiags)
	}
	return val, diags
}

func (r *resolver) stringValue(val cty.Value, expr hcl.Expression, ident configs.StaticIdentifier) (string, bool) {
	if val.IsMarked() {
		r.errorf(expr.Range(), "Identity derived from a sensitive value",
			"%s is derived from a sensitive value. An import identity is written to logs and plan output, so it cannot be sensitive. If the value is not genuinely secret - a data source such as tfe_outputs that marks its whole result sensitive is the common case - wrap it in nonsensitive(...) to use it here.", ident.Subject)
		return "", false
	}
	if val.IsNull() {
		r.errorf(expr.Range(), "Null identity argument",
			"%s evaluated to null, which cannot be part of an import identity.", ident.Subject)
		return "", false
	}
	if !val.IsWhollyKnown() {
		r.errorf(expr.Range(), "Non-static identity argument",
			"%s cannot be evaluated from configuration alone. Every part of an identity must be a constant, or derived from variables, locals and pure functions, or a reference to another resource's identity attribute. A function that returns a different value on each call - uuid(), timestamp(), bcrypt() - evaluates to an unknown value here, including when it is reached through a local or written in .tf.json.", ident.Subject)
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		r.errorf(expr.Range(), "Non-string identity argument",
			"%s cannot be used as part of an import identity: %s.", ident.Subject, err)
		return "", false
	}
	return str.AsString(), true
}

func (r *resolver) identifier(addr addrs.AbsResourceInstance, attrName string, rng hcl.Range) configs.StaticIdentifier {
	return configs.StaticIdentifier{
		Module:    addr.Module.Module(),
		Subject:   fmt.Sprintf("%s.%s", addr.String(), attrName),
		DeclRange: rng,
	}
}

// resourceCloudScope is [Resolution.cloudScope]'s whole implementation: a
// [cloudScopeKey] that agrees for two resources only when they plausibly
// target the same account and region, so [resolver.checkCollisions] can
// tell a genuine duplicate-identity collision from two same-named
// resources that simply live in different places.
//
// It has two independent inputs, both general across every managed
// resource type and every provider, not just AWS:
//
//   - The resource's resolved absolute provider configuration
//     ([providerscope.ResolveResource]), which already walks every
//     enclosing module call's own `providers = { ... }` mapping. Two
//     module calls of the same child module, each remapping `aws` to a
//     different aliased provider block, are exactly this: same resource
//     address shape inside the module, different account or region
//     outside it (found in the corpus: simpleinfra's dev-desktops calls
//     ./aws-region once per region this way).
//   - A literal `region` argument set directly on the resource body, read
//     the same PartialContent-then-static-evaluate way every identity
//     argument already is. This one is AWS-specific in practice - it is
//     the per-resource region override the AWS provider has exposed on
//     almost every resource type since it adopted the endpoints
//     framework - but nothing here names a resource type to reach it: any
//     resource in any provider that happens to declare a statically
//     known `region` argument gets the same treatment. (Found in the
//     corpus: govuk-infrastructure's chat estate declares the identical
//     aws_cloudwatch_log_group name "/aws/bedrock" twice, once with
//     region = "eu-west-1" and once with region = "eu-west-2" - the same
//     provider configuration both times, since neither block uses a
//     provider alias, so only the region argument tells them apart.)
//
// The provider configuration always contributes [cloudScopeKey.base], which
// [resolver.checkCollisions] requires to match exactly. The region comes
// from [resolver.effectiveRegion] - the literal argument when the resource
// states one, the resolved provider configuration's own static `region`
// otherwise (#217) - and lands in [cloudScopeKey.region]/regionKnown, which
// checkCollisions treats altogether differently: regionKnown false is a
// wildcard that never rules out a collision on its own, so a resource this
// function could not place in any region is exactly as suspect a duplicate
// as it always was, not silently cleared by an unrelated sibling's known
// one. A resource in a single-region, single-account estate never states a
// region anywhere this can find one, so every instance shares base alone
// with regionKnown false - the collision key in that case reduces to
// exactly what it was before this existed, Type+identity, with no drop in
// sensitivity.
//
// This is deliberately best-effort in the OTHER direction: a `region`
// argument that fails to evaluate statically (references something a plain
// identity argument could not either) is silently treated as unknown
// rather than refused, because collision detection choosing not to
// disambiguate a resource pair is strictly safer than a spurious refusal
// over an argument nothing else in this run needed to read. See
// [resolver.staticRegionAttr] and [resolver.providerRegionAttr].
func (r *resolver) resourceCloudScope(rc *configs.Resource, scope instScope) cloudScopeKey {
	abs := providerscope.ResolveResource(r.curCfg, rc)
	region, ok := r.effectiveRegion(rc, abs, scope)
	return cloudScopeKey{base: abs.String(), region: region, regionKnown: ok}
}

// effectiveRegion is [resolver.resourceCloudScope]'s own region component
// (#217): the resource's own literal `region` argument when it states one,
// falling back to the region its resolved provider configuration itself
// declares statically when it does not. Two resources of the same identity
// that both target the same effective region must produce the same scope
// string regardless of which of them spelled the region out - a resource
// stating `region = "us-east-1"` and its sibling silently inheriting the
// identical region from the enclosing `provider "aws"` block collided
// before #217's own regression and must keep colliding now. ok is false,
// and no region joins the scope at all, whenever NEITHER side resolves
// statically: [resolver.staticRegionAttr]'s and
// [resolver.providerRegionAttr]'s own doc comments explain why that must
// fall back to "no override" rather than a refusal - the same reasoning
// extends here in the direction #217 asks for, since an unknown region on
// either side of a comparison must collide (report) rather than silently
// distinguish two resources that might be identical.
func (r *resolver) effectiveRegion(rc *configs.Resource, abs addrs.AbsProviderConfig, scope instScope) (string, bool) {
	if region, ok := r.staticRegionAttr(rc, scope); ok {
		return region, true
	}
	return r.providerRegionAttr(abs)
}

// staticRegionAttr reads a resource's own `region` argument, when the body
// sets one and it evaluates to a known, non-null string from configuration
// alone. It never records a diagnostic: unlike every other argument this
// package reads, a `region` override is consulted only to sharpen
// [resolver.checkCollisions], not to build the identity itself, so a
// failure here must fall back to "no override", not refuse the run.
func (r *resolver) staticRegionAttr(rc *configs.Resource, scope instScope) (string, bool) {
	content, _, diags := rc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "region"}},
	})
	if diags.HasErrors() {
		return "", false
	}
	attr, ok := content.Attributes["region"]
	if !ok || r.isSymbolic(attr.Expr, scope) {
		return "", false
	}
	ident := configs.StaticIdentifier{
		Module:    r.modInst.Module(),
		Subject:   fmt.Sprintf("%s.region", rc.Addr().String()),
		DeclRange: attr.Range,
	}
	val, evalDiags := r.evalPure(attr.Expr, scope, ident)
	if evalDiags.HasErrors() || val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return "", false
	}
	return str.AsString(), true
}

// providerRegionAttr reads abs's own provider configuration block's
// `region` argument, when one exists and it evaluates to a known, non-null
// string from configuration alone - [resolver.effectiveRegion]'s fallback
// for a resource whose own body states no `region` override, so that
// resource inherits the region its provider block would actually apply
// (#217). [providerscope.Resolve] always anchors abs at the root module
// (see that function's own doc comment: every return path sets
// Module: addrs.RootModule), so the search below only ever looks at
// r.rootCfg's own module, never the module the resolver happens to be
// walking when this is called.
//
// The match against abs is the same deterministic shape
// internal/live/dataread/analyze.go's findProviderConfig already uses for
// the identical problem (an [addrs.AbsProviderConfig] naming a provider
// type by its fully-qualified source, a config block naming it by local
// name): iterate the module's ProviderConfigs in sorted key order,
// filtering first by Alias and then by comparing
// [configs.Module.ProviderForLocalConfig] of the block's own local name
// against abs.Provider. Never [configs.Module.LocalNameForProvider] run in
// the other direction - that lookup is keyed by the very FQN this function
// is trying to resolve and picks one winner out of a Go map with no stable
// order, which is exactly the nondeterminism a lookup keyed by the thing
// being resolved produces (a real defect elsewhere in this codebase, not a
// hypothetical one).
//
// Like [resolver.staticRegionAttr], this never records a diagnostic and
// never treats an unresolvable region as anything other than "no
// override" - a provider block this function cannot resolve, a `region`
// this run cannot statically evaluate, or a provider configured with its
// own `for_each` (whose `region` may differ per key, which this function
// declines to guess at) all report ok=false, which [resolver.effectiveRegion]
// (and #217's own safety direction) treats as "cannot distinguish these two
// resources by region", not as a value of "no region".
func (r *resolver) providerRegionAttr(abs addrs.AbsProviderConfig) (string, bool) {
	if r.rootCfg == nil || r.rootCfg.Module == nil {
		return "", false
	}
	mod := r.rootCfg.Module

	keys := make([]string, 0, len(mod.ProviderConfigs))
	for k := range mod.ProviderConfigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var pc *configs.Provider
	for _, k := range keys {
		cand := mod.ProviderConfigs[k]
		if cand.Alias != abs.Alias {
			continue
		}
		if mod.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: cand.Name}) != abs.Provider {
			continue
		}
		pc = cand
		break
	}
	if pc == nil || pc.Config == nil || pc.ForEach != nil {
		return "", false
	}

	content, _, diags := pc.Config.PartialContent(&hcl.BodySchema{
		Attributes: []hcl.AttributeSchema{{Name: "region"}},
	})
	if diags.HasErrors() {
		return "", false
	}
	attr, ok := content.Attributes["region"]
	if !ok || r.isSymbolic(attr.Expr, instScope{}) {
		return "", false
	}

	// A provider block's own expressions live in, and reference variables
	// and locals of, the ROOT module - which very often differs from
	// whatever module the resolver is currently walking a resource in
	// ([resolver.mod]'s own doc comment). r.eval is swapped to the root
	// module's own pure evaluator for the one call below and restored
	// immediately after; nothing else in this function or its caller
	// depends on r.eval, so the swap has no visible effect beyond it.
	saved := r.eval
	r.eval = mod.StaticEvaluator.Pure()
	defer func() { r.eval = saved }()

	ident := configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   fmt.Sprintf("provider.%s.region", pc.Name),
		DeclRange: attr.Range,
	}
	val, evalDiags := r.evalPure(attr.Expr, instScope{}, ident)
	if evalDiags.HasErrors() || val.IsMarked() || val.IsNull() || !val.IsWhollyKnown() {
		return "", false
	}
	str, err := convert.Convert(val, cty.String)
	if err != nil {
		return "", false
	}
	return str.AsString(), true
}

func (r *resolver) errorf(rng hcl.Range, summary, format string, args ...any) {
	diag := &hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  summary,
		Detail:   fmt.Sprintf(format, args...),
		Subject:  rng.Ptr(),
	}
	if r.curInstanceAddr != "" {
		// Freshly built, so there is nothing to preserve: unlike
		// [resolver.appendDiags], no wrap is needed here.
		diag.Extra = InstanceFailure{Addr: r.curInstanceAddr}
	}
	r.diags = r.diags.Append(diag)
}

// appendDiags is [resolver.errorf] for diagnostics this package did not
// itself construct - an [addrs.ParseRef] failure, an hcl.Body's own
// PartialContent diagnostics, [resolver.evalPure]'s. It takes the same
// argument shape [tfdiags.Diagnostics.Append] itself does (hcl.Diagnostics,
// *hcl.Diagnostic, tfdiags.Diagnostics, ...) rather than picking one, since
// its callers pass whichever shape their own diagnostics already came back
// as.
//
// Every diagnostic gets the same [InstanceFailure] tag errorf attaches,
// wrapping whatever Extra it already carries rather than discarding it (see
// [instanceFailureDiag] and [InstanceFailure.UnwrapDiagnosticExtra]) -
// otherwise a data-source reference refused inside a failing component
// would lose the [configs.RefusedReference] internal/live/dataread and
// internal/live/check's classifyDataSite both depend on.
func (r *resolver) appendDiags(new ...any) {
	if r.curInstanceAddr == "" {
		r.diags = r.diags.Append(new...)
		return
	}
	var toTag tfdiags.Diagnostics
	toTag = toTag.Append(new...)
	if len(toTag) == 0 {
		return
	}
	tagged := make(tfdiags.Diagnostics, len(toTag))
	for i, d := range toTag {
		tagged[i] = instanceFailureDiag{Diagnostic: d, tag: InstanceFailure{Addr: r.curInstanceAddr, inner: d.ExtraInfo()}}
	}
	r.diags = r.diags.Append(tagged)
}

// instanceFailureDiag decorates a [tfdiags.Diagnostic] with an
// [InstanceFailure], leaving everything else about it - including whatever
// Extra it already carried, reachable through InstanceFailure's own unwrap
// chain - untouched. See [resolver.appendDiags].
type instanceFailureDiag struct {
	tfdiags.Diagnostic
	tag InstanceFailure
}

func (d instanceFailureDiag) ExtraInfo() interface{} { return d.tag }

// ---- expansion -------------------------------------------------------

// expansion is how a resource block expands into instances, plus whatever
// each instance needs in scope to evaluate its own arguments.
type expansion struct {
	keys []addrs.InstanceKey

	// counted is set when the expansion came from count, so that
	// count.index is in scope.
	counted bool

	// eachValues holds each.value per key for a for_each over a static
	// collection. Nil when for_each iterates over another resource.
	eachValues map[addrs.InstanceKey]cty.Value

	// eachParent is set when for_each iterates over another managed
	// resource: each.value is then that resource's instance with the same
	// key, which is a symbolic reference rather than a value.
	eachParent *addrs.Resource

	// keyOnly marks an expansion built by #178's key-set fix
	// (staticForEachKeys in localvalue.go): the for_each source is an
	// object constructor whose keys are statically known but whose values
	// are not - typically a managed resource's attribute reached through
	// one of them - so eachValues is deliberately left nil rather than
	// populated with a guess. each.key resolves normally, at any depth
	// (directly, or reached through a local's own definition - #213);
	// each.value, wherever it is read, is not covered by
	// [instances.RepetitionData.EachValue] and refuses cleanly with
	// "Dynamic value in static context" ([configs.StaticEvaluator]'s own
	// diagnostic, not a recovered panic - see [resolver.evalPure]).
	// Resolving each.value symbolically in this position - the for_each
	// half of the local-values fix, as opposed to the plain-reference half
	// [resolver.namedLeaf] builds - is a further extension this fix does
	// not make.
	keyOnly bool
}

func (e *expansion) hasKey(key addrs.InstanceKey) bool {
	for _, k := range e.keys {
		if k == key {
			return true
		}
	}
	return false
}

// describe explains an expansion in an error about a bad instance
// reference.
func (e *expansion) describe(res addrs.Resource) string {
	if len(e.keys) == 0 {
		return fmt.Sprintf("%s expands to no instances at all.", res.String())
	}
	strs := make([]string, 0, len(e.keys))
	for _, k := range e.keys {
		strs = append(strs, res.Instance(k).String())
	}
	return fmt.Sprintf("%s expands to: %s.", res.String(), strings.Join(strs, ", "))
}

// scope builds the per-instance evaluation scope for key: the same
// each.key/each.value/count.index this instance's own arguments already
// see, carried as [instances.RepetitionData] so a local's own definition -
// reached transitively, not only referenced directly - sees exactly the
// same values (see [instScope.repetition] and #213). A field left at
// cty.NilVal below is deliberate, not an oversight: each.value under
// e.eachParent or e.keyOnly is symbolic (this package refuses to evaluate
// it, and resolves each.value.<attr> structurally instead - see
// [resolver.resolveTraversal]), so leaving RepetitionData.EachValue unset
// makes a bare each.value reached through a local refuse exactly as a
// direct one already does, rather than fabricate a value nothing here
// actually knows.
func (e *expansion) scope(key addrs.InstanceKey) instScope {
	sc := instScope{key: key, eachParent: e.eachParent}
	switch {
	case e.counted:
		idx, ok := key.(addrs.IntKey)
		if !ok {
			return sc
		}
		sc.repetition = instances.RepetitionData{CountIndex: cty.NumberIntVal(int64(idx))}
	case e.eachValues != nil:
		sc.repetition = instances.RepetitionData{EachKey: keyValue(key), EachValue: e.eachValues[key]}
	case e.eachParent != nil:
		// each.value is symbolic here, so only each.key has a value; a
		// reference to each.value is handled structurally instead.
		sc.repetition = instances.RepetitionData{EachKey: keyValue(key)}
	case e.keyOnly:
		// Same shape as the eachParent case above, for the same reason:
		// only the key was ever knowable without evaluating a value this
		// package refuses to evaluate.
		sc.repetition = instances.RepetitionData{EachKey: keyValue(key)}
	}
	return sc
}

// instScope is the per-instance evaluation scope: the instance key, the
// each.key/each.value/count.index values that are known (handed to
// [configs.StaticEvaluator.WithRepetitionData] so they reach a local's own
// definition, not only the top-level expression - see #213), any other
// top-level binding this package's own for-comprehension handling supplies
// (a loop variable name, never "each" or "count"), and the parent resource
// that each.value stands for when it is not known.
type instScope struct {
	key        addrs.InstanceKey
	repetition instances.RepetitionData
	vars       map[string]cty.Value
	eachParent *addrs.Resource
}

func (r *resolver) expansionFor(rc *configs.Resource) (*expansion, bool) {
	key := r.expKey(rc)
	if exp, ok := r.expansions[key]; ok {
		return exp, true
	}
	if r.expFailed[key] {
		return nil, false
	}
	if r.expVisit[key] {
		// rc.Addr(), not key: expKey joins the module instance and the
		// address with a NUL byte, which rendered into the diagnostic as a
		// control character ahead of the address. resolve.go's other cycle
		// message (Circular identity reference) uses the address directly
		// and always has. Found by the #101 message audit.
		r.errorf(rc.DeclRange, "Circular for_each reference",
			"The instances of %s depend on themselves, directly or through other resources' for_each expressions.", rc.Addr().String())
		r.expFailed[key] = true
		return nil, false
	}
	r.expVisit[key] = true
	defer delete(r.expVisit, key)

	exp, ok := r.buildExpansion(rc)
	if !ok {
		r.expFailed[key] = true
		return nil, false
	}
	r.expansions[key] = exp
	return exp, true
}

func (r *resolver) buildExpansion(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()

	switch {
	case rc.Count != nil:
		return r.countExpansion(rc)

	case rc.ForEach != nil:
		return r.forEachExpansion(rc)

	case rc.Enabled != nil:
		ident := r.moduleIdentifier(addr.String()+" lifecycle.enabled", rc.Enabled.Range())
		val, ok := r.evalStatic(rc.Enabled, instScope{}, ident)
		if !ok {
			return nil, false
		}
		if val.IsMarked() {
			// Same crash as count above, one branch further down: b.False()
			// asserts the value is unmarked and panics when it is not.
			// internal/command/e2etest/testdata/ephemeral-repetition/enabled
			// is the reproducing configuration.
			r.errorf(rc.Enabled.Range(), "Sensitive lifecycle.enabled expression",
				"Whether %s exists is decided by a sensitive or ephemeral value. Existence decides which markers this run writes and which live objects it claims, so it cannot come from a value this run may not record.", addr.String())
			return nil, false
		}
		b, err := convert.Convert(val, cty.Bool)
		if err != nil || !b.IsKnown() || b.IsNull() {
			r.errorf(rc.Enabled.Range(), "Non-static lifecycle.enabled expression",
				"Whether %s exists cannot be determined from configuration alone, so its instances cannot be enumerated.", addr.String())
			return nil, false
		}
		if b.False() {
			return &expansion{}, true
		}
		return &expansion{keys: []addrs.InstanceKey{addrs.NoKey}}, true

	default:
		return &expansion{keys: []addrs.InstanceKey{addrs.NoKey}}, true
	}
}

// countExpansion resolves rc.Count. It is the same evaluation buildExpansion
// always did, except that length(<resource>) - the whole expression, or
// nested under a comparison, a ternary, or parentheses, the shapes count
// actually uses in the corpus (#178) - is rewritten to that resource's own
// already-computed instance count before the generic static evaluator ever
// sees it. That is the count analogue of forEachOverResource: the keys come
// from the resolver's own expansion of the referenced resource, never from
// evaluating the referenced resource's attributes.
func (r *resolver) countExpansion(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()
	expr := rc.Count
	if rewritten, changed, ok := r.rewriteResourceLength(expr); changed {
		// changed is true only when a length(<resource>) call was actually
		// matched, so !ok here always means that resource's own expansion
		// failed - the error is already on r.diags (see expansionFor and
		// the cycle guard above it), and there is nothing more to say.
		// A resource reference in a shape this does not recognize comes
		// back as changed=false instead, expr unmodified, and falls
		// through to evalStatic below, which raises the ordinary "Dynamic
		// value in static context" refusal for it - the honest answer,
		// since this rule explains length(<resource>), not every shape
		// that might wrap one.
		if !ok {
			return nil, false
		}
		expr = rewritten
	}

	ident := r.moduleIdentifier(addr.String()+" count", rc.Count.Range())
	val, ok := r.evalStatic(expr, instScope{}, ident)
	if !ok {
		return nil, false
	}
	if val.IsMarked() {
		// Before the marked check, gocty.FromCtyValue below panicked
		// ("value is marked, so must be unmarked first") and took the
		// whole run down. An ephemeral variable in count is the shortest
		// way there - internal/command/e2etest/testdata/
		// ephemeral-repetition/count is exactly that configuration - and
		// a sensitive one reaches it too. for_each already refused its
		// own marked value a few lines below; count did not.
		r.errorf(rc.Count.Range(), "Sensitive count expression",
			"The count for %s is sensitive or ephemeral, so the instance keys it produces cannot become part of resource addresses. Addresses are written to markers, logs and plan output.", addr.String())
		return nil, false
	}
	if !val.IsKnown() || val.IsNull() {
		r.errorf(rc.Count.Range(), "Non-static count expression",
			"The count for %s evaluated to null, or to a value not knowable from configuration alone. Instance keys are the addresses a projection binds against, so a count has to be a whole number this run can compute before anything is read from the cloud; guessing a cardinality would silently drop or invent instances.", addr.String())
		return nil, false
	}
	num, err := convert.Convert(val, cty.Number)
	if err != nil {
		r.errorf(rc.Count.Range(), "Invalid count",
			"The count for %s is not a number: %s.", addr.String(), err)
		return nil, false
	}
	var n int
	if err := gocty.FromCtyValue(num, &n); err != nil {
		r.errorf(rc.Count.Range(), "Invalid count",
			"The count for %s is not a whole number: %s.", addr.String(), err)
		return nil, false
	}
	if n < 0 {
		r.errorf(rc.Count.Range(), "Invalid count",
			"The count for %s is negative.", addr.String())
		return nil, false
	}
	exp := &expansion{counted: true}
	for i := 0; i < n; i++ {
		exp.keys = append(exp.keys, addrs.IntKey(i))
	}
	return exp, true
}

// lengthOfResource reports whether expr is exactly length(<resource>): a
// call to the length function whose single argument is a bare reference to
// a whole managed resource that itself uses count or for_each. Only a
// multi-instance resource makes length(<resource>) mean "how many
// instances there are": OpenTofu evaluates length() of a single-instance
// resource reference over that resource's own object attributes instead, a
// different number this resolver has no way to reproduce without a schema
// read, so that case is left alone for the generic evaluator's own
// refusal rather than guessed at.
func (r *resolver) lengthOfResource(expr hcl.Expression) (*configs.Resource, bool) {
	call, diags := hcl.ExprCall(expr)
	if diags.HasErrors() {
		return nil, false
	}
	if call.Name != "length" || len(call.Arguments) != 1 {
		return nil, false
	}
	trav, diags := hcl.AbsTraversalForExpr(call.Arguments[0])
	if diags.HasErrors() {
		return nil, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		return nil, false
	}
	resAddr, ok := ref.Subject.(addrs.Resource)
	if !ok || len(ref.Remaining) > 0 || resAddr.Mode != addrs.ManagedResourceMode {
		return nil, false
	}
	parentRC := r.mod.ResourceByAddr(resAddr)
	if parentRC == nil || (parentRC.Count == nil && parentRC.ForEach == nil) {
		return nil, false
	}
	return parentRC, true
}

// rewriteResourceLength looks for length(<resource>) inside expr and
// replaces each one with that resource's own already-computed instance
// count from expansionFor - the memoized, cycle-guarded expansion every
// other resource's count and for_each already goes through. It recurses
// through parentheses, ternaries and comparisons, because those are the
// wrapped forms count actually uses in the corpus (#178); it does not
// invent handling for shapes that are not there.
//
// changed reports whether expr referenced a managed resource at all. When
// changed is false, rewritten is expr itself and ok is always true: the
// caller can evaluate it exactly as before, because nothing here applies.
// When changed is true, ok is false in two different situations the caller
// must tell apart: a matched length(<resource>) call whose resource failed
// to resolve (the failure is already on r.diags - give up, do not fall
// back), or a resource reference in a shape this function does not
// recognize (nothing has been reported yet - the caller should evaluate the
// original expr through the generic path instead, which raises the
// ordinary "Dynamic value in static context" refusal for it).
func (r *resolver) rewriteResourceLength(expr hcl.Expression) (rewritten hcl.Expression, changed bool, ok bool) {
	if !r.isSymbolic(expr, instScope{}) {
		return expr, false, true
	}

	if parentRC, isLen := r.lengthOfResource(expr); isLen {
		parentExp, expOK := r.expansionFor(parentRC)
		if !expOK {
			return nil, true, false
		}
		return &hclsyntax.LiteralValueExpr{
			Val:      cty.NumberIntVal(int64(len(parentExp.keys))),
			SrcRange: expr.Range(),
		}, true, true
	}

	switch e := expr.(type) {
	case *hclsyntax.ParenthesesExpr:
		inner, ch, ok := r.rewriteResourceLength(e.Expression)
		if !ok {
			return nil, ch, false
		}
		cp := *e
		cp.Expression = inner.(hclsyntax.Expression)
		return &cp, true, true

	case *hclsyntax.ConditionalExpr:
		cond, ch, ok := r.rewriteResourceLength(e.Condition)
		if !ok {
			return nil, ch, false
		}
		tr, ch, ok := r.rewriteResourceLength(e.TrueResult)
		if !ok {
			return nil, ch, false
		}
		fr, ch, ok := r.rewriteResourceLength(e.FalseResult)
		if !ok {
			return nil, ch, false
		}
		cp := *e
		cp.Condition = cond.(hclsyntax.Expression)
		cp.TrueResult = tr.(hclsyntax.Expression)
		cp.FalseResult = fr.(hclsyntax.Expression)
		return &cp, true, true

	case *hclsyntax.BinaryOpExpr:
		lhs, ch, ok := r.rewriteResourceLength(e.LHS)
		if !ok {
			return nil, ch, false
		}
		rhs, ch, ok := r.rewriteResourceLength(e.RHS)
		if !ok {
			return nil, ch, false
		}
		cp := *e
		cp.LHS = lhs.(hclsyntax.Expression)
		cp.RHS = rhs.(hclsyntax.Expression)
		return &cp, true, true
	}

	// isSymbolic is true but expr is none of the recognized wrapper shapes:
	// a bare reference, an attribute traversal, a different function, a
	// for expression, and so on. Left unrecognized on purpose - the caller
	// falls back to the generic evaluator's own message rather than a
	// bespoke one here.
	return nil, false, false
}

func (r *resolver) forEachExpansion(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()
	expr := rc.ForEach

	// for_each over another resource: the keys are that resource's keys,
	// which is config data even though the values are not.
	if r.isSymbolic(expr, instScope{}) {
		if fe, ok := unwrapForExpr(expr); ok {
			if got, exOK, applicable := r.forEachOverComprehension(rc, fe); applicable {
				return got, exOK
			}
		}
		// toset([for x in <static collection> : <expr>]) - a keyless
		// (tuple-producing) comprehension, the idiomatic way to turn a
		// for-expression into the set for_each requires. Distinct from the
		// case just above: there, the collection clause IS the resource
		// being iterated; here, the collection is ordinary static data and
		// a sibling reference, if any, lives in the per-element value
		// clause instead. See [resolver.forEachOverTupleComprehension].
		if inner, ok := unwrapToSet(expr); ok {
			if fe, ok := unwrapForExpr(inner); ok && fe.KeyExpr == nil {
				if got, exOK, applicable := r.forEachOverTupleComprehension(rc, fe); applicable {
					return got, exOK
				}
			}
		}
		return r.forEachOverResource(rc)
	}

	ident := r.moduleIdentifier(addr.String()+" for_each", expr.Range())
	mark := len(r.diags)
	val, ok := r.evalStatic(expr, instScope{}, ident)
	if !ok {
		// #178's key-set fix: an object constructor's key set is knowable
		// whatever its values are, and the key set is all a for_each
		// expansion needs to enumerate instances - a resource reference
		// buried in one of the VALUES must not refuse the whole block the
		// way evaluating the object as a single value just did. Tried only
		// after the whole-value evaluation above has already failed, so a
		// for_each that already worked keeps going through the unchanged
		// path below, with eachValues populated the way every consumer of
		// it already expects. See localvalue.go.
		if keys, structOK := r.staticForEachKeys(expr, ident, 0); structOK {
			r.diags = r.diags[:mark]
			sorted := append([]string(nil), keys...)
			sort.Strings(sorted)
			exp := &expansion{keyOnly: true}
			for _, name := range sorted {
				exp.keys = append(exp.keys, addrs.StringKey(name))
			}
			return r.checkedForEachKeys(rc, exp)
		}
		return nil, false
	}
	if !val.IsWhollyKnown() || val.IsNull() {
		r.errorf(expr.Range(), "Non-static for_each expression",
			"The for_each value for %s cannot be determined from configuration alone. Instance keys are the addresses a projection binds against, so they must be knowable before anything is read from the cloud.", addr.String())
		return nil, false
	}
	if val.IsMarked() {
		r.errorf(expr.Range(), "Sensitive for_each expression",
			"The for_each value for %s is sensitive, so it cannot become part of resource addresses.", addr.String())
		return nil, false
	}

	ty := val.Type()
	exp := &expansion{eachValues: make(map[addrs.InstanceKey]cty.Value)}
	switch {
	case ty.IsMapType(), ty.IsObjectType():
		elems := make(map[string]cty.Value)
		var names []string
		for it := val.ElementIterator(); it.Next(); {
			k, v := it.Element()
			name := k.AsString()
			names = append(names, name)
			elems[name] = v
		}
		sort.Strings(names)
		for _, name := range names {
			k := addrs.StringKey(name)
			exp.keys = append(exp.keys, k)
			exp.eachValues[k] = elems[name]
		}
		return r.checkedForEachKeys(rc, exp)

	case ty.IsSetType():
		// An empty set built from a for-expression with no matching source
		// elements - toset([for x in var.y : x if <false for everything>]),
		// the shape a filtered comprehension produces whenever nothing
		// passes the filter - carries cty.DynamicPseudoType as its element
		// type, because cty has nothing to infer a concrete one from. Stock
		// OpenTofu's own for_each validation accepts that element type
		// alongside cty.String (internal/lang/evalchecks/eval_for_each.go's
		// performSetTypeChecks); this package's own check did not, so an
		// empty for_each set refused here even though OpenTofu itself
		// accepts it downstream. Checked as emptiness rather than the type
		// directly, since a zero-length set has no keys to enumerate either
		// way and the distinction does not matter to this package.
		if val.LengthInt() == 0 {
			return r.checkedForEachKeys(rc, exp)
		}
		if ty.ElementType() != cty.String {
			r.errorf(expr.Range(), "Invalid for_each set",
				"The for_each value for %s is a set of %s. Only a set of strings can produce instance keys.", addr.String(), ty.ElementType().FriendlyName())
			return nil, false
		}
		var names []string
		for it := val.ElementIterator(); it.Next(); {
			_, v := it.Element()
			names = append(names, v.AsString())
		}
		sort.Strings(names)
		for _, name := range names {
			k := addrs.StringKey(name)
			exp.keys = append(exp.keys, k)
			exp.eachValues[k] = cty.StringVal(name)
		}
		return r.checkedForEachKeys(rc, exp)

	default:
		r.errorf(expr.Range(), "Invalid for_each value",
			"The for_each value for %s is %s. for_each accepts a map, an object, or a set of strings.", addr.String(), ty.FriendlyName())
		return nil, false
	}
}

// forEachOverResource handles `for_each = <other resource>`: the fixture's
// aws_route_table_association.this iterating over aws_subnet.this. The keys
// come from the parent's own expansion, so they are knowable even though
// every value in the parent is a live ID.
func (r *resolver) forEachOverResource(rc *configs.Resource) (*expansion, bool) {
	addr := rc.Addr()
	expr := rc.ForEach

	parentAddr, parentExp, ok := r.resolveResourceRef(expr, addr.String(), expr.Range())
	if !ok {
		return nil, false
	}

	parent := parentAddr
	return &expansion{
		keys:       append([]addrs.InstanceKey(nil), parentExp.keys...),
		eachParent: &parent,
	}, true
}

// resolveResourceRef resolves expr as a bare reference to a whole managed
// resource - for_each = aws_subnet.this itself, or the collection clause of
// a for-comprehension over one (forEachOverComprehension below) - into that
// resource's own already-computed expansion. subject and rng scope the
// diagnostic to whichever site actually wrote the reference.
func (r *resolver) resolveResourceRef(expr hcl.Expression, subject string, rng hcl.Range) (addrs.Resource, *expansion, bool) {
	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() {
		r.errorf(rng, "Non-static for_each expression",
			"The for_each value for %s is computed from another resource's attributes. Only a plain reference to another resource (for_each = aws_subnet.this) can have its instance keys resolved from configuration; anything computed from resource attributes is known only after the cloud is read.", subject)
		return addrs.Resource{}, nil, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		r.appendDiags(refDiags)
		return addrs.Resource{}, nil, false
	}
	parentAddr, ok := ref.Subject.(addrs.Resource)
	if !ok || len(ref.Remaining) > 0 || parentAddr.Mode != addrs.ManagedResourceMode {
		r.errorf(rng, "Non-static for_each expression",
			"The for_each value for %s refers to %s. Instance keys can be propagated only from a whole managed resource (for_each = aws_subnet.this).", subject, ref.Subject.String())
		return addrs.Resource{}, nil, false
	}

	parentRC := r.mod.ResourceByAddr(parentAddr)
	if parentRC == nil {
		r.errorf(rng, "Reference to undeclared resource",
			"The for_each value for %s refers to %s, which is not declared in this configuration.", subject, parentAddr.String())
		return addrs.Resource{}, nil, false
	}
	parentExp, ok := r.expansionFor(parentRC)
	if !ok {
		return addrs.Resource{}, nil, false
	}
	if parentExp.eachValues == nil && parentExp.eachParent == nil && !parentExp.keyOnly {
		r.errorf(rng, "for_each over a resource that is not keyed",
			"The for_each value for %s is %s, which does not use for_each, so it is not a map of instances. OpenTofu accepts only a map or a set of strings as a for_each argument.", subject, parentAddr.String())
		return addrs.Resource{}, nil, false
	}
	return parentAddr, parentExp, true
}

// forEachOverComprehension recognizes `for_each = { for k, v in <resource> :
// keyExpr => valExpr if cond }`: a for-comprehension whose collection is a
// bare reference to another managed resource. It resolves when the key
// clause and the "if" filter depend only on the comprehension's own key
// variable and on locally-evaluable data, never on the value variable -
// the same test [resolver.evalPure]'s marked-value handling already makes
// unnecessary to guess at, because it is checked structurally, on
// expr.Variables(), rather than by evaluating anything the parent keeps
// symbolic. The parent's own already-computed expansion.keys is the only
// input this needs; it never looks at what a value var would carry.
//
// applicable is false whenever the shape does not match at all: not an
// object-producing for-expression (for_each never accepts the tuple a
// key-less for-expression produces directly), or a collection clause that
// is not a bare reference to a for_each'd managed resource. The caller
// falls back to forEachOverResource's own diagnostic on the whole
// expression in that case - the same "computed from another resource's
// attributes" message a comprehension already got before this function
// existed. When applicable is true, ok reports whether resolution actually
// succeeded, and a diagnostic has already been recorded in its place when
// it did not.
func (r *resolver) forEachOverComprehension(rc *configs.Resource, fe *hclsyntax.ForExpr) (exp *expansion, ok bool, applicable bool) {
	if fe.KeyExpr == nil {
		// A key-less for-expression produces a tuple, never a map or object;
		// for_each cannot take that shape directly (only wrapped in
		// toset(...), a different top-level expression this function is
		// never reached for). Nothing here applies to it.
		return nil, false, false
	}

	if _, diags := hcl.AbsTraversalForExpr(fe.CollExpr); diags.HasErrors() {
		// The collection clause is not a bare reference to begin with - some
		// other expression this function does not recognize, symbolic for a
		// reason unrelated to "for_each over a resource". Not this shape:
		// stay silent and let the whole-expression fallback in
		// forEachExpansion raise its own diagnostic, the same one a
		// comprehension always got before this function existed.
		return nil, false, false
	}

	addr := rc.Addr()
	subject := addr.String()
	rng := rc.ForEach.Range()

	// The collection clause may be a sibling resource's own literal
	// argument rather than the resource itself - `for domain in
	// fastly_tls_subscription.subscription.domains : ...` (GitHub issue
	// #220), as opposed to `for k, v in aws_subnet.this : ...` below. Tried
	// first because it is the more specific shape: a bare resource
	// reference has no remaining traversal steps for this to match, so the
	// two never compete for the same expression.
	if got, ok, applicable := r.comprehensionOverSiblingAttr(rc, fe, subject, rng); applicable {
		return got, ok, true
	}

	// The collection clause looks like a plain resource reference, so this
	// IS the shape from here on: every failure below is this function's to
	// report, not forEachOverResource's, or the site would be counted (and
	// diagnosed) twice for the one for_each argument that produced it.
	_, parentExp, resolved := r.resolveResourceRef(fe.CollExpr, subject, rng)
	if !resolved {
		return nil, false, true
	}

	if fe.ValVar != "" && (refsRoot(fe.KeyExpr, fe.ValVar) || (fe.CondExpr != nil && refsRoot(fe.CondExpr, fe.ValVar))) {
		r.errorf(rng, "Non-static for_each expression",
			"The for_each value for %s is a comprehension whose key or filter reads %s, its own iterated value. Only the key and locally-evaluable data can decide which instances exist and what they are named; a value read from the iterated resource is known only after the cloud is read.", subject, fe.ValVar)
		return nil, false, true
	}

	ident := r.moduleIdentifier(subject+" for_each", rng)
	seen := map[string]bool{}
	var names []string
	for _, pk := range parentExp.keys {
		var scope instScope
		if fe.KeyVar != "" {
			scope.vars = map[string]cty.Value{fe.KeyVar: keyValue(pk)}
		}
		if fe.CondExpr != nil {
			condVal, condOK := r.evalStatic(fe.CondExpr, scope, ident)
			if !condOK {
				return nil, false, true
			}
			include, err := convert.Convert(condVal, cty.Bool)
			if err != nil || include.IsNull() || !include.IsKnown() {
				r.errorf(fe.CondExpr.Range(), "Invalid for_each condition",
					"The if clause of %s's for_each comprehension did not evaluate to a known boolean.", subject)
				return nil, false, true
			}
			if include.False() {
				continue
			}
		}
		keyVal, keyOK := r.evalStatic(fe.KeyExpr, scope, ident)
		if !keyOK {
			return nil, false, true
		}
		ks, err := convert.Convert(keyVal, cty.String)
		if err != nil || ks.IsNull() || !ks.IsKnown() {
			r.errorf(fe.KeyExpr.Range(), "Invalid for_each key",
				"The key clause of %s's for_each comprehension did not evaluate to a known string.", subject)
			return nil, false, true
		}
		name := ks.AsString()
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	sort.Strings(names)
	built := &expansion{keyOnly: true}
	for _, n := range names {
		built.keys = append(built.keys, addrs.StringKey(n))
	}
	result, checkedOK := r.checkedForEachKeys(rc, built)
	return result, checkedOK, true
}

// comprehensionOverSiblingAttr recognizes `for_each = { for x in
// <sibling>.<attr> : keyExpr => valExpr }`, where <attr> is not the sibling
// resource itself (that shape is [resolver.forEachOverComprehension]'s own,
// via [resolver.resolveResourceRef]) but one of the sibling's own literal
// arguments - GitHub issue #220's fastly_tls_subscription.subscription.domains,
// where domains is set verbatim from var.domains. The rule that makes
// reading it safe is [resolver.siblingLiteralExpr]'s: it is the same one
// [resolver.parentPart] already applies to a single identity component,
// used here for a for_each's own collection clause instead.
//
// Deliberately unconcerned with valExpr: only the key clause and the "if"
// filter decide which instances exist, the same restriction
// [resolver.forEachOverComprehension] already enforces for a for_each over
// a whole resource, and for the identical reason - the value clause is
// read only through each.value once an instance exists, and this
// package's own keyOnly expansion already refuses a bare each.value
// cleanly rather than guess at it (see [expansion.keyOnly]). This is
// exactly what lets fastly-tls-subscription's own valExpr reach into
// managed_dns_challenges, a genuinely apply-time attribute, without that
// blocking the for_each itself.
//
// applicable is false whenever the collection clause is not this shape at
// all: not a single-attribute traversal into a resource, or an attribute
// [resolver.siblingLiteralExpr] cannot confirm is a plain literal. The
// caller falls back to [resolver.resolveResourceRef]'s own handling (and
// its own diagnostic) in that case, unchanged.
func (r *resolver) comprehensionOverSiblingAttr(rc *configs.Resource, fe *hclsyntax.ForExpr, subject string, rng hcl.Range) (exp *expansion, ok bool, applicable bool) {
	trav, diags := hcl.AbsTraversalForExpr(fe.CollExpr)
	if diags.HasErrors() {
		return nil, false, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		return nil, false, false
	}
	var parentInstAddr addrs.ResourceInstance
	switch subj := ref.Subject.(type) {
	case addrs.Resource:
		parentInstAddr = subj.Instance(addrs.NoKey)
	case addrs.ResourceInstance:
		parentInstAddr = subj
	default:
		return nil, false, false
	}
	if parentInstAddr.Resource.Mode != addrs.ManagedResourceMode || len(ref.Remaining) != 1 {
		return nil, false, false
	}
	attrStep, isAttr := ref.Remaining[0].(hcl.TraverseAttr)
	if !isAttr {
		return nil, false, false
	}

	parent := parentInstAddr.Absolute(r.modInst)
	collExpr, collScope, exprApplicable := r.siblingLiteralExpr(parent, attrStep.Name)
	if !exprApplicable {
		return nil, false, false
	}

	// This IS the shape from here on: every failure below is this
	// function's to report.
	ident := r.moduleIdentifier(subject+" for_each", rng)
	collVal, collOK := r.evalStatic(collExpr, collScope, ident)
	if !collOK {
		return nil, false, true
	}
	if !collVal.IsWhollyKnown() || collVal.IsNull() {
		r.errorf(fe.CollExpr.Range(), "Non-static for_each expression",
			"The for_each value for %s iterates over %s, which cannot be determined from configuration alone.", subject, traversalString(trav))
		return nil, false, true
	}
	ty := collVal.Type()
	if !ty.IsListType() && !ty.IsSetType() && !ty.IsTupleType() && !ty.IsMapType() && !ty.IsObjectType() {
		r.errorf(fe.CollExpr.Range(), "Non-static for_each expression",
			"The for_each value for %s iterates over %s, which is %s, not a collection.", subject, traversalString(trav), ty.FriendlyName())
		return nil, false, true
	}

	seen := map[string]bool{}
	var names []string
	for it := collVal.ElementIterator(); it.Next(); {
		_, v := it.Element()
		scope := instScope{}
		if fe.ValVar != "" {
			scope.vars = map[string]cty.Value{fe.ValVar: v}
		}
		if fe.CondExpr != nil {
			condVal, condOK := r.evalStatic(fe.CondExpr, scope, ident)
			if !condOK {
				return nil, false, true
			}
			include, err := convert.Convert(condVal, cty.Bool)
			if err != nil || include.IsNull() || !include.IsKnown() {
				r.errorf(fe.CondExpr.Range(), "Invalid for_each condition",
					"The if clause of %s's for_each comprehension did not evaluate to a known boolean.", subject)
				return nil, false, true
			}
			if include.False() {
				continue
			}
		}
		keyVal, keyOK := r.evalStatic(fe.KeyExpr, scope, ident)
		if !keyOK {
			return nil, false, true
		}
		ks, err := convert.Convert(keyVal, cty.String)
		if err != nil || ks.IsNull() || !ks.IsKnown() {
			r.errorf(fe.KeyExpr.Range(), "Invalid for_each key",
				"The key clause of %s's for_each comprehension did not evaluate to a known string.", subject)
			return nil, false, true
		}
		name := ks.AsString()
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	sort.Strings(names)
	built := &expansion{keyOnly: true}
	for _, n := range names {
		built.keys = append(built.keys, addrs.StringKey(n))
	}
	result, checkedOK := r.checkedForEachKeys(rc, built)
	return result, checkedOK, true
}

// forEachOverTupleComprehension recognizes `for_each = toset([for x in
// <collection> : <expr>])`: a keyless (tuple-producing) for-expression
// wrapped in toset(), the idiomatic way to turn a for-expression into the
// set for_each requires - GitHub issue #220's govuk-infrastructure
// aws_ecr_lifecycle_policy, whose for_each is
// `toset([for repo in local.lifecycle_policy_repositories :
// aws_ecr_repository.github_repositories[repo].name])`.
//
// Unlike [resolver.comprehensionOverSiblingAttr], the collection here is
// never a sibling at all: it is ordinary static data (a local, a
// variable, a literal list) that alone decides how many instances exist.
// A symbolic reference belongs in the per-element expression instead,
// exactly where the govuk-infrastructure site puts it, and
// [resolver.resolveExpr] is what already resolves such a reference for an
// identity component - reused here unchanged, because indexing into a
// sibling by the comprehension's own loop variable
// (aws_ecr_repository.github_repositories[repo]) is the identical shape
// [resolver.resolveIndexedTraversal] already handles.
//
// Every resolved element must still be a plain literal: a for_each key
// can never wait on an apply-time value, so an element whose resolution
// comes back parent-derived (the sibling's own argument was itself a
// reference to something genuinely computed) refuses here rather than
// silently degrading to a formula no for_each key can be.
//
// applicable is false only when the collection clause itself is symbolic
// - referencing a managed resource - because then the SHAPE of the
// for_each, not just one string inside it, would depend on a value this
// package cannot read before the cloud does: the same refusal
// [resolver.forEachOverResource] already gives a for_each that names a
// resource directly.
func (r *resolver) forEachOverTupleComprehension(rc *configs.Resource, fe *hclsyntax.ForExpr) (exp *expansion, ok bool, applicable bool) {
	if r.isSymbolic(fe.CollExpr, instScope{}) {
		return nil, false, false
	}

	addr := rc.Addr()
	subject := addr.String()
	rng := rc.ForEach.Range()
	ident := r.moduleIdentifier(subject+" for_each", rng)

	collVal, collOK := r.evalStatic(fe.CollExpr, instScope{}, ident)
	if !collOK {
		return nil, false, true
	}
	if !collVal.IsWhollyKnown() || collVal.IsNull() {
		r.errorf(fe.CollExpr.Range(), "Non-static for_each expression",
			"The for_each value for %s cannot be determined from configuration alone.", subject)
		return nil, false, true
	}
	ty := collVal.Type()
	if !ty.IsListType() && !ty.IsSetType() && !ty.IsTupleType() {
		r.errorf(fe.CollExpr.Range(), "Invalid for_each value",
			"The for_each value for %s iterates over a value of type %s, not a collection.", subject, ty.FriendlyName())
		return nil, false, true
	}

	seen := map[string]bool{}
	var names []string
	for it := collVal.ElementIterator(); it.Next(); {
		_, v := it.Element()
		scope := instScope{}
		if fe.ValVar != "" {
			scope.vars = map[string]cty.Value{fe.ValVar: v}
		}
		if fe.CondExpr != nil {
			condVal, condOK := r.evalStatic(fe.CondExpr, scope, ident)
			if !condOK {
				return nil, false, true
			}
			include, err := convert.Convert(condVal, cty.Bool)
			if err != nil || include.IsNull() || !include.IsKnown() {
				r.errorf(fe.CondExpr.Range(), "Invalid for_each condition",
					"The if clause of %s's for_each comprehension did not evaluate to a known boolean.", subject)
				return nil, false, true
			}
			if include.False() {
				continue
			}
		}
		parts, valOK := r.resolveExpr(fe.ValExpr, scope, ident)
		if !valOK {
			return nil, false, true
		}
		coalesced := coalesce(parts)
		if len(coalesced) != 1 || coalesced[0].Parent != nil {
			r.errorf(fe.ValExpr.Range(), "Non-static for_each expression",
				"The for_each value for %s is a comprehension whose element depends on a value that is not known until apply.", subject)
			return nil, false, true
		}
		name := coalesced[0].Literal
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}

	sort.Strings(names)
	built := &expansion{keyOnly: true}
	for _, n := range names {
		built.keys = append(built.keys, addrs.StringKey(n))
	}
	result, checkedOK := r.checkedForEachKeys(rc, built)
	return result, checkedOK, true
}

// unwrapToSet reports whether expr is a call to toset() with exactly one
// argument, returning that argument - the wrapper
// [resolver.forEachOverTupleComprehension]'s own shape is written through,
// since a keyless for-expression produces a tuple and for_each accepts
// only a map or a set.
func unwrapToSet(expr hcl.Expression) (hcl.Expression, bool) {
	call, ok := expr.(*hclsyntax.FunctionCallExpr)
	if !ok || call.Name != "toset" || len(call.Args) != 1 {
		return nil, false
	}
	return call.Args[0], true
}

// unwrapForExpr reports whether expr is, ignoring surrounding parentheses, a
// *hclsyntax.ForExpr - the shape forEachOverComprehension knows how to read.
func unwrapForExpr(expr hcl.Expression) (*hclsyntax.ForExpr, bool) {
	for {
		switch e := expr.(type) {
		case *hclsyntax.ParenthesesExpr:
			expr = e.Expression
		case *hclsyntax.ForExpr:
			return e, true
		default:
			return nil, false
		}
	}
}

// refsRoot reports whether expr references name as the root of one of its
// free variables - the same expr.Variables() traversal isSymbolic already
// reads, just checked against a single name instead of a fixed keyword set.
func refsRoot(expr hcl.Expression, name string) bool {
	for _, trav := range expr.Variables() {
		if trav.RootName() == name {
			return true
		}
	}
	return false
}

func (r *resolver) moduleIdentifier(subject string, rng hcl.Range) configs.StaticIdentifier {
	return configs.StaticIdentifier{
		Module:    r.modInst.Module(),
		Subject:   subject,
		DeclRange: rng,
	}
}

// ---- small helpers ---------------------------------------------------

func sortedResources(resources map[string]*configs.Resource) []*configs.Resource {
	out := make([]*configs.Resource, 0, len(resources))
	for _, rc := range resources {
		out = append(out, rc)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Addr().String() < out[j].Addr().String()
	})
	return out
}

func firstPresent(attrs hcl.Attributes, names []string) *hcl.Attribute {
	for _, n := range names {
		if a, ok := attrs[n]; ok {
			return a
		}
	}
	return nil
}

// firstPrefixSibling is [firstPresent] for the "<name>_prefix" convention
// [resolver.identityArgs] pulls alongside every plain name: it reports the
// first one actually set in the resource body, and which of names it is a
// sibling of, so the caller can name the base argument in its own message.
func firstPrefixSibling(attrs hcl.Attributes, names []string) (attr *hcl.Attribute, base string) {
	for _, n := range names {
		if a, ok := attrs[n+"_prefix"]; ok {
			return a, n
		}
	}
	return nil, ""
}

// coalesce merges adjacent literal parts, so that a formula's parts
// alternate literal and parent as far as possible.
func coalesce(parts []Part) []Part {
	out := make([]Part, 0, len(parts))
	for _, p := range parts {
		if p.Parent == nil && len(out) > 0 && out[len(out)-1].Parent == nil {
			out[len(out)-1].Literal += p.Literal
			continue
		}
		out = append(out, p)
	}
	return out
}

func keyValue(key addrs.InstanceKey) cty.Value {
	switch k := key.(type) {
	case addrs.StringKey:
		return cty.StringVal(string(k))
	case addrs.IntKey:
		return cty.NumberIntVal(int64(k))
	default:
		return cty.NullVal(cty.String)
	}
}

func isAttrStep(step hcl.Traverser, name string) bool {
	attr, ok := step.(hcl.TraverseAttr)
	return ok && attr.Name == name
}

func traversalString(trav hcl.Traversal) string {
	ref, diags := addrs.ParseRef(trav)
	if diags.HasErrors() || ref == nil {
		return trav.RootName()
	}
	return ref.DisplayString()
}

func orList(names []string) string {
	quoted := quoteAll(names)
	switch len(quoted) {
	case 0:
		return "(none)"
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + ", or " + quoted[len(quoted)-1]
	}
}

func quoteAll(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, fmt.Sprintf("%q", n))
	}
	return out
}
