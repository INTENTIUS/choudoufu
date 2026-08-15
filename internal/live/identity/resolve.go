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
	"github.com/intentius/choudoufu/internal/lang"
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
		child := cfg.Children[name]
		var forEach hcl.Expression
		if call, ok := r.mod.ModuleCalls[name]; ok && call != nil {
			forEach = call.ForEach
		}
		keys, diag := ChildModuleKeys(r.ctx, r.mod, childSubject(name), forEach)
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
	seen := make(map[string]addrs.AbsResourceInstance)
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
		key := res.Type() + "\x00" + ident

		first, exists := seen[key]
		if !exists {
			seen[key] = res.Addr
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
			first.String(), res.Addr.String(), ident)
	}
}

func newResolver(ctx context.Context, cfg *configs.Config, rctx Context) *resolver {
	r := &resolver{
		ctx:        ctx,
		rootCfg:    cfg,
		cloud:      rctx.Cloud,
		schemas:    rctx.Schemas,
		expansions: make(map[string]*expansion),
		expFailed:  make(map[string]bool),
		expVisit:   make(map[string]bool),
		insts:      make(map[string]Resolution),
		instFailed: make(map[string]bool),
		instVisit:  make(map[string]bool),
		synth:      make(map[string]*TypeIdentity),
	}
	r.enterModule(cfg)
	return r
}

type resolver struct {
	ctx context.Context

	// rootCfg is the configuration [Resolve] was given, kept so that a
	// parent-derived reference or a resolved instance address can look up
	// any node of the static module tree by its module path. See
	// [resolver.enterModuleFor].
	rootCfg *configs.Config

	// mod, modInst and eval are the module currently being worked on: the
	// module whose resources [resolver.expansionFor] and
	// [resolver.resolveInstance] are reading. They are mutated by
	// [resolver.enterModule] as the walk moves between modules, so nothing
	// in this package may cache them across a call that might change
	// modules.
	mod     *configs.Module
	modInst addrs.ModuleInstance
	eval    *configs.StaticEvaluator

	cloud CloudContext
	diags tfdiags.Diagnostics

	// schemas are the provider's resource type schemas when the caller had
	// them, and nil when it did not. See [Context.Schemas].
	schemas map[string]providers.Schema

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
	r.modInst = modInst
	// Pure on purpose: an identity is a claim about which cloud object a
	// block owns, and a function that answers differently every time it is
	// called cannot make that claim. See impure.go.
	r.eval = cfg.Module.StaticEvaluator.Pure()
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
			r.errorf(rc.DeclRange, "Identity argument not set",
				"%s has no value for %s, so its import identity (%s) cannot be built.",
				addr.String(), orList(comp.Attrs), entry.ImportSyntax)
			return Resolution{}, false
		}
		ident := r.identifier(addr, attr.Name, attr.Range)
		got, ok := r.resolveExpr(attr.Expr, scope, ident)
		if !ok {
			return Resolution{}, false
		}
		parts = append(parts, got...)
		addTo(comp.identityAttrFor(attr.Name), got)
	}

	return classify(addr, coalesce(parts), attrFormulas(byAttr, attrOrder), entry.IdentityObjectOnly), true
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
	for _, comp := range entry.Components {
		for _, n := range comp.Attrs {
			if !seen[n] {
				seen[n] = true
				names = append(names, n)
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
		r.diags = r.diags.Append(diags)
		return nil, false
	}
	return content.Attributes, true
}

// resolveExpr turns one argument expression into import-ID parts.
func (r *resolver) resolveExpr(expr hcl.Expression, scope instScope, ident configs.StaticIdentifier) ([]Part, bool) {
	if !r.isSymbolic(expr, scope) {
		val, ok := r.evalStatic(expr, scope, ident)
		if !ok {
			return nil, false
		}
		s, ok := r.stringValue(val, expr, ident)
		if !ok {
			return nil, false
		}
		return []Part{{Literal: s}}, true
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
		r.errorf(expr.Range(), "Identity not resolvable from configuration",
			"%s refers to another resource inside an expression that identity resolution cannot follow. "+
				"A resource reference contributes to an identity only as a whole reference or as an interpolation in a string template; "+
				"it cannot be passed through functions or operators, because the value it produces is not known until apply.",
			ident.Subject)
		return nil, false
	}
	return r.resolveTraversal(trav, scope, ident)
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
		r.diags = r.diags.Append(refDiags)
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
		r.diags = r.diags.Append(diags)
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
// each.key or each.value - and internal/configs' static scope has no
// repetition data to answer that with; it panics ("Not Available in Static
// Context") rather than erroring. This package never evaluates such an
// expression on purpose (see [ChildModuleKeys]'s doc: a module call's own
// for_each is evaluated in its parent's scope, never a child's variables),
// but nothing stops a resource argument from referencing one anyway, and a
// crash here would take the whole run down over one identity component this
// package was always going to refuse. Degrading to a clean "cannot
// evaluate" is the same choice [lint.evalStatic] already makes for the
// class of panic it guards against.
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
		switch trav.RootName() {
		case "each", "count":
			// Supplied by the instance scope below; the static evaluator
			// panics on repetition references, so they must not reach it.
			continue
		}
		travs = append(travs, trav)
	}

	refs, refDiags := lang.References(addrs.ParseRef, travs)
	if refDiags.HasErrors() {
		return cty.NilVal, diags.Append(refDiags)
	}

	hclCtx, ctxDiags := r.eval.EvalContext(r.ctx, ident, refs)
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
			"%s is derived from a sensitive value. An import identity is written to logs and plan output, so it cannot be sensitive.", ident.Subject)
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

func (r *resolver) errorf(rng hcl.Range, summary, format string, args ...any) {
	r.diags = r.diags.Append(&hcl.Diagnostic{
		Severity: hcl.DiagError,
		Summary:  summary,
		Detail:   fmt.Sprintf(format, args...),
		Subject:  rng.Ptr(),
	})
}

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

func (e *expansion) scope(key addrs.InstanceKey) instScope {
	sc := instScope{key: key, eachParent: e.eachParent}
	switch {
	case e.counted:
		idx, ok := key.(addrs.IntKey)
		if !ok {
			return sc
		}
		sc.vars = map[string]cty.Value{
			"count": cty.ObjectVal(map[string]cty.Value{
				"index": cty.NumberIntVal(int64(idx)),
			}),
		}
	case e.eachValues != nil:
		sc.vars = map[string]cty.Value{
			"each": cty.ObjectVal(map[string]cty.Value{
				"key":   keyValue(key),
				"value": e.eachValues[key],
			}),
		}
	case e.eachParent != nil:
		// each.value is symbolic here, so only each.key has a value; a
		// reference to each.value is handled structurally instead.
		sc.vars = map[string]cty.Value{
			"each": cty.ObjectVal(map[string]cty.Value{
				"key": keyValue(key),
			}),
		}
	}
	return sc
}

// instScope is the per-instance evaluation scope: the instance key, the
// repetition values that are known, and the parent resource that each.value
// stands for when it is not known.
type instScope struct {
	key        addrs.InstanceKey
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
		return r.forEachOverResource(rc)
	}

	ident := r.moduleIdentifier(addr.String()+" for_each", expr.Range())
	val, ok := r.evalStatic(expr, instScope{}, ident)
	if !ok {
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

	trav, diags := hcl.AbsTraversalForExpr(expr)
	if diags.HasErrors() {
		r.errorf(expr.Range(), "Non-static for_each expression",
			"The for_each value for %s is computed from another resource's attributes. Only a plain reference to another resource (for_each = aws_subnet.this) can have its instance keys resolved from configuration; anything computed from resource attributes is known only after the cloud is read.", addr.String())
		return nil, false
	}
	ref, refDiags := addrs.ParseRef(trav)
	if refDiags.HasErrors() {
		r.diags = r.diags.Append(refDiags)
		return nil, false
	}
	parentAddr, ok := ref.Subject.(addrs.Resource)
	if !ok || len(ref.Remaining) > 0 || parentAddr.Mode != addrs.ManagedResourceMode {
		r.errorf(expr.Range(), "Non-static for_each expression",
			"The for_each value for %s refers to %s. Instance keys can be propagated only from a whole managed resource (for_each = aws_subnet.this).", addr.String(), ref.Subject.String())
		return nil, false
	}

	parentRC := r.mod.ResourceByAddr(parentAddr)
	if parentRC == nil {
		r.errorf(expr.Range(), "Reference to undeclared resource",
			"The for_each value for %s refers to %s, which is not declared in this configuration.", addr.String(), parentAddr.String())
		return nil, false
	}
	parentExp, ok := r.expansionFor(parentRC)
	if !ok {
		return nil, false
	}
	if parentExp.eachValues == nil && parentExp.eachParent == nil {
		r.errorf(expr.Range(), "for_each over a resource that is not keyed",
			"The for_each value for %s is %s, which does not use for_each, so it is not a map of instances. OpenTofu accepts only a map or a set of strings as a for_each argument.", addr.String(), parentAddr.String())
		return nil, false
	}

	parent := parentAddr
	return &expansion{
		keys:       append([]addrs.InstanceKey(nil), parentExp.keys...),
		eachParent: &parent,
	}, true
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
