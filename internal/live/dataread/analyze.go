// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package dataread

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/live/identity"
	"github.com/intentius/choudoufu/internal/providers"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// Options is what a caller may tell the analysis about the world outside
// the configuration. Everything is optional; the zero value analyzes with
// the configuration alone.
type Options struct {
	// Schemas are the provider's managed resource type schemas, the same
	// map every resolution caller already has ([identity.Context.Schemas]).
	// The analysis probes identity resolution to learn which data sources
	// it demands, and the probe should admit the same types the real
	// resolution will, or a type the schemas admit would hide the demand
	// behind its own refusal.
	Schemas map[string]providers.Schema

	// ProjectManagedArguments is issue #193's fix class (c), PROTOTYPE and
	// OFF by default: answer a managed resource attribute reference from
	// the resource block's own configuration argument, offline, instead of
	// refusing it as dynamic. Off, this package behaves exactly as it did
	// before the option existed. See managedproj.go for what is and is not
	// built.
	ProjectManagedArguments bool
}

// Source is one data resource block the analysis classified: demanded by an
// identity-bearing position (directly, or transitively through another data
// source), and either readable before the plan or refused with a reason.
type Source struct {
	// Module is the module path declaring the block, unkeyed.
	Module addrs.Module

	// Resource is the module-relative data resource address.
	Resource addrs.Resource

	// Config is the declaring block.
	Config *configs.Resource

	// NeededBy names what demanded this source: the identity-bearing
	// identifier whose evaluation referenced it, or the demanding data
	// source for a transitive dependency.
	NeededBy string

	// TfeOutputs marks a tfe_outputs source. It goes through the same
	// eligibility pipeline as a same-stack source. Credential presence
	// itself is not an eligibility question - the maintainer's ruling on
	// #181 (eligibility models the owner, consistent with stage 1's
	// treatment of the aws provider block): a token argument, TFE_TOKEN, or
	// a CLI credentials entry is assumed to exist for the owner running
	// this, and its actual absence surfaces as a read-time refusal under
	// [SummaryCrossStackOutputsUnavailable] instead (see [reader.readSource]
	// in read.go).
	TfeOutputs bool

	// RemoteState marks a terraform_remote_state source. #179 stage 3 gives
	// it the same eligibility pipeline a same-stack source gets: its own
	// arguments (the backend type, its config object, the workspace) must
	// be statically evaluable, the same rules 1/2/4 any data source draws.
	// Backend credentials are assumed present for eligibility, the same
	// ruling [Source.TfeOutputs] documents; an absent, unreachable, or
	// undecodable backend refuses honestly at read time under
	// [SummaryCrossStackStateUnavailable].
	RemoteState bool

	// Eligible reports that the phase can read this source before the plan:
	// static arguments and count/for_each, a statically configurable
	// provider, and no managed-resource dependency.
	Eligible bool

	// PerInstance reports that at least one of this block's own arguments -
	// directly, or inside a nested block - reads this same block's own
	// count.index or each.key/each.value. That is ordinary per-block
	// repetition scoping, the same scoping every resource and data block
	// gets in stock OpenTofu (internal/tofu/evaluate.go's
	// evaluationStateData.GetCountAttr/GetForEachAttr, bound per instance
	// from the block's own expansion), not a dynamic value: each instance's
	// key or value is known the moment the block's own count/for_each is
	// known, the same round this analysis already evaluates it in.
	//
	// It means instances of this block can have genuinely different
	// arguments, so [Read] cannot share one provider answer across every
	// instance the way it does for a block whose arguments are
	// instance-invariant; it reads each instance separately instead, with
	// that instance's own count.index or each.key/each.value bound - one
	// provider call per instance, the same cost stock OpenTofu's own
	// per-instance data-source read already pays
	// (internal/tofu/node_resource_abstract_instance.go, readDataSource).
	PerInstance bool

	// ReasonSummary and ReasonDetail are the refusal for an ineligible
	// source: ReasonSummary is one of this package's Summary constants and
	// ReasonDetail the class-specific sentence.
	ReasonSummary string
	ReasonDetail  string

	// Deps are the data sources this one references, directly, through
	// locals, in depends_on, or - since #212 - through a module-call
	// variable that reaches back into an ancestor module's own data source:
	// Dep.Module names the DECLARING module, which is this Source's own
	// Module for an ordinary same-module reference and an ancestor's for a
	// cross-module one. These are the edges the topological read order
	// follows. Sorted.
	Deps []SourceDep
}

// SourceDep is one dependency edge: the module and resource of a data
// source [Source.Deps]'s owner references. Module can differ from the
// referencing Source's own Module - see [Source.Deps]'s doc.
type SourceDep struct {
	Module   addrs.Module
	Resource addrs.Resource
}

func (d SourceDep) key() string { return sourceKey(d.Module, d.Resource) }

// Analysis is [Analyze]'s result: every demanded data source, classified,
// with the readable ones in an order that reads dependencies first.
type Analysis struct {
	sources map[string]*Source

	// order holds every demanded source, dependencies before dependents -
	// the property [Read] relies on. Deterministic across runs.
	order []*Source
}

// Empty reports that identity resolution demands no data sources at all,
// which is every configuration that worked before this phase existed: the
// phase then costs nothing.
func (a *Analysis) Empty() bool { return a == nil || len(a.order) == 0 }

// Demanded returns every demanded source, dependencies before dependents.
func (a *Analysis) Demanded() []*Source {
	if a == nil {
		return nil
	}
	return a.order
}

// SourceFor returns the classification for one data resource, when it was
// demanded.
func (a *Analysis) SourceFor(module addrs.Module, res addrs.Resource) (*Source, bool) {
	if a == nil {
		return nil, false
	}
	src, ok := a.sources[sourceKey(module, res)]
	return src, ok
}

func sourceKey(module addrs.Module, res addrs.Resource) string {
	return module.String() + "\x00" + res.String()
}

// moduleLabel names a module for a refusal sentence: "the root module" or
// "module.a.b", matching how a user would name it in configuration.
func moduleLabel(module addrs.Module) string {
	if len(module) == 0 {
		return "the root module"
	}
	return module.String()
}

// maxForEachAliasDepth bounds how many locals or merge() arguments
// [analyzer.forEachDataRefs] will chase through before giving up, the same
// backstop [maxStaticDecomposeDepth] gives resolve.go's own version of this
// walk (internal/live/identity/localvalue.go) - a defensive bound against a
// pathological or self-referential chain, not a limit ordinary
// configurations approach.
const maxForEachAliasDepth = 16

// scanForEachDataRefs finds every data-source reference reachable from ANY
// resource block's own for_each meta-argument, across every module in the
// tree, and classifies each one demanded - independent of whatever the
// diagnostic-probing loop in [Analyze] would or would not have found on its
// own.
//
// It exists because [resolver.forEachExpansion] (resolve.go) recovers from
// a for_each whose KEYS are statically known but whose VALUES are not by
// discarding the whole-value evaluation's diagnostics and building a
// "keyOnly" expansion instead (#178's key-set fix, in localvalue.go's
// staticForEachKeys/objectConsKeys) - deliberately: a resource reference
// buried in one value must not refuse the whole block when only the key
// set is needed to enumerate instances. The diagnostic that discarding
// throws away is exactly the one [demandRoots] otherwise relies on to
// discover a data source reference. A configuration that reads
// each.key alone never needed it. One whose own identity-bearing
// argument reads each.value - govuk-infrastructure's
// aws_security_group_rule.postgres_from_eks_workers is the corpus's one
// confirmed instance, #209 - needs the discarded value's data source read
// before the plan exactly as much as a direct reference would, and had no
// way to ask for it: by the time each.value is evaluated, the object
// constructor that named the data source is gone, replaced by an opaque
// per-instance value with no attribute named "value" at all (see
// resolve.go's expansion.keyOnly and #213, which turned that shape's
// raw HCL "Unsupported attribute" into the cleaner but still
// data-source-silent "Dynamic value in static context").
//
// This does not need resolve.go to change at all: once the source found
// here is read, [Read] populates [identity.Context.DataResults] with its
// real value, the for_each's whole-value evaluation succeeds on
// resolve.go's ordinary first attempt (the value is now wholly known,
// never a placeholder), eachValues is populated the regular way, and
// each.value resolves like any other for_each over a static map - no
// keyOnly fallback, no special-casing, because the expression that used to
// fail now simply does not.
//
// Only the shapes [resolver.staticForEachKeys] itself recognizes are
// walked - an object constructor, one level of local aliasing, or merge()
// of such - because those are exactly the shapes that can reach the
// keyOnly fallback in the first place; a for_each in any other shape either
// evaluates as a whole (a direct data reference stays visible to
// [demandRoots] the ordinary way, see aws_security_group_rule's own
// asset-master-efs/elasticsearch/shared_docdb/licensify_docdb siblings in
// the same corpus file) or fails outright with no fallback to swallow its
// diagnostic. Module-call variable aliasing (for_each = var.name reaching a
// literal in a calling module) is not chased: unlike [staticForEachKeys],
// which the resolver runs per instance and can therefore navigate to the
// exact calling module instance, this scan runs once over the static
// module tree with no instance in hand, and no such shape appeared in the
// corpus scan that measured #209 (only a same-module local and a same-file
// object literal did). A for_each aliased that way keeps today's behavior:
// undetected, exactly as every other unrecognized shape here is left
// alone.
func (an *analyzer) scanForEachDataRefs() {
	an.walkModulesForForEach(an.cfg)
}

func (an *analyzer) walkModulesForForEach(cfg *configs.Config) {
	if cfg == nil || cfg.Module == nil {
		return
	}
	mod := cfg.Module
	resources := make([]*configs.Resource, 0, len(mod.ManagedResources)+len(mod.DataResources))
	for _, rc := range mod.ManagedResources {
		resources = append(resources, rc)
	}
	for _, rc := range mod.DataResources {
		resources = append(resources, rc)
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Addr().String() < resources[j].Addr().String() })

	for _, rc := range resources {
		if rc.ForEach == nil {
			continue
		}
		for _, res := range an.forEachDataRefs(mod, rc.ForEach, 0) {
			// The block's own for_each over its own data source's
			// nonexistent-instance shape (for_each = data.x.y ranging over
			// itself) cannot occur - a data block cannot reference its own
			// for_each in its own for_each - so no self-dependency guard is
			// needed the way [classify]'s own "record" callback needs one.
			an.classify(cfg.Path, res, fmt.Sprintf("%s's for_each", rc.Addr().String()))
		}
	}

	names := make([]string, 0, len(cfg.Children))
	for name := range cfg.Children {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		an.walkModulesForForEach(cfg.Children[name])
	}
}

// forEachDataRefs finds every data-source reference in expr's object-value
// positions, following the same shapes [resolver.staticForEachKeys] chases
// in resolve.go before deciding a for_each's keys are knowable regardless
// of its values: parentheses, one level of local aliasing within mod, and
// merge() of any of those. Anything else - a for-expression, a function
// other than merge, a bare data reference with no surrounding object
// constructor - is left alone, matching that function's own "not
// applicable" behavior for a shape it does not recognize; those shapes
// either already surface their data reference through the ordinary
// diagnostic-probing path or genuinely have none to find.
func (an *analyzer) forEachDataRefs(mod *configs.Module, expr hcl.Expression, depth int) []addrs.Resource {
	if depth > maxForEachAliasDepth {
		return nil
	}
	if paren, ok := expr.(*hclsyntax.ParenthesesExpr); ok {
		return an.forEachDataRefs(mod, paren.Expression, depth+1)
	}
	if trav, diags := hcl.AbsTraversalForExpr(expr); !diags.HasErrors() && len(trav) == 2 && trav.RootName() == "local" {
		if nameStep, ok := trav[1].(hcl.TraverseAttr); ok {
			if local, ok := mod.Locals[nameStep.Name]; ok {
				return an.forEachDataRefs(mod, local.Expr, depth+1)
			}
		}
	}
	if call, ok := expr.(*hclsyntax.FunctionCallExpr); ok && call.Name == "merge" {
		var out []addrs.Resource
		for _, arg := range call.Args {
			out = append(out, an.forEachDataRefs(mod, arg, depth+1)...)
		}
		return out
	}
	obj, ok := expr.(*hclsyntax.ObjectConsExpr)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	var out []addrs.Resource
	for _, item := range obj.Items {
		for _, trav := range item.ValueExpr.Variables() {
			if trav.RootName() != "data" {
				continue
			}
			ref, diags := addrs.ParseRef(trav)
			if diags.HasErrors() {
				continue
			}
			res, ok := DataSubject(ref.Subject)
			if !ok {
				continue
			}
			key := res.String()
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, res)
		}
	}
	return out
}

// Analyze derives which data sources identity resolution demands and
// classifies each as readable-before-the-plan or not. It is offline: no
// provider process, no cloud call, nothing but the configuration - which is
// what lets live-check run it under its no-cloud-calls contract.
//
// Demand is discovered by probing resolution itself rather than by
// reimplementing its notion of an identity-bearing position: resolution is
// run with placeholder coverage for the data sources found so far, every
// data-source refusal it still raises names a newly demanded source (the
// structured [configs.RefusedReference] carries which), and the loop
// repeats until resolution demands nothing new. The probe's diagnostics are
// discarded - the real resolution runs later, with real values.
func Analyze(ctx context.Context, cfg *configs.Config, opts Options) *Analysis {
	a := &Analysis{sources: make(map[string]*Source)}
	if cfg == nil || cfg.Module == nil || cfg.Module.StaticEvaluator == nil {
		return a
	}
	an := &analyzer{ctx: ctx, cfg: cfg, analysis: a, schemas: opts.Schemas, projectManaged: opts.ProjectManagedArguments, visiting: make(map[string]bool), managedCache: make(map[string]managedProj)}

	// #209: a data source reference nested inside a for_each meta-argument's
	// own map-value literal never surfaces in a probe round's diagnostics at
	// all - see [analyzer.scanForEachDataRefs] for why - so it has to be
	// found by reading the for_each expressions directly, once, before the
	// diagnostic-driven loop below ever runs. Every source this finds is
	// classified exactly like a directly-referenced one; the loop's own
	// placeholder coverage and transitive-dependency handling apply to it
	// unchanged from here on.
	an.scanForEachDataRefs()

	placeholders := make(map[string]cty.Value)
	// Each productive round classifies at least one new source or extends
	// placeholder coverage by at least one instance; both are bounded by the
	// configuration, and the loop stops the first round that does neither.
	// The cap is a defensive backstop, not the termination rule.
	for round := 0; round < 1000; round++ {
		_, diags := identity.ResolveWith(ctx, cfg, identity.Context{
			Schemas:     an.schemas,
			DataResults: placeholders,
		})
		fresh := false
		for _, root := range an.demandRoots(diags) {
			if _, seen := a.sources[sourceKey(root.module, root.resource)]; seen {
				continue
			}
			if an.classify(root.module, root.resource, root.neededBy) != nil {
				fresh = true
			}
		}
		if !an.extendPlaceholders(placeholders) && !fresh {
			break
		}
	}
	return a
}

type analyzer struct {
	ctx      context.Context
	cfg      *configs.Config
	analysis *Analysis
	schemas  map[string]providers.Schema
	visiting map[string]bool

	// projectManaged is [Options.ProjectManagedArguments].
	projectManaged bool

	// managedCache is #193's PROTOTYPE fix class (c) memo.
	managedCache map[string]managedProj
}

type demandRoot struct {
	module   addrs.Module
	resource addrs.Resource
	neededBy string
}

// demandRoots reads the newly demanded data sources out of a probe run's
// refusals: every dynamic-value refusal whose structured extra names a
// same-stack data source. Cross-stack refusals are collected too - the
// demand is real and live-check reports on it - but their classification
// never marks them readable.
func (an *analyzer) demandRoots(diags tfdiags.Diagnostics) []demandRoot {
	var roots []demandRoot
	for _, diag := range diags {
		if diag.Severity() != tfdiags.Error {
			continue
		}
		ref := tfdiags.ExtraInfo[configs.RefusedReference](diag)
		switch ref.Category {
		case configs.CategoryDataSource, configs.CategoryTfeOutputs, configs.CategoryRemoteState:
		default:
			continue
		}
		res, ok := DataSubject(ref.Subject)
		if !ok {
			continue
		}
		roots = append(roots, demandRoot{module: ref.Module, resource: res, neededBy: ref.NeededBy})
	}
	sort.Slice(roots, func(i, j int) bool {
		return sourceKey(roots[i].module, roots[i].resource) < sourceKey(roots[j].module, roots[j].resource)
	})
	return roots
}

// DataSubject extracts the containing data resource from a reference's
// subject, when it names one. Exported for the check layer, which uses it
// to map a refusal site back to the data source this analysis classified.
func DataSubject(subject addrs.Referenceable) (addrs.Resource, bool) {
	switch s := subject.(type) {
	case addrs.Resource:
		if s.Mode == addrs.DataResourceMode {
			return s, true
		}
	case addrs.ResourceInstance:
		if s.Resource.Mode == addrs.DataResourceMode {
			return s.ContainingResource(), true
		}
	}
	return addrs.Resource{}, false
}

// extendPlaceholders covers every classified source - same-stack or either
// cross-stack flavor, both now carried through the same pipeline - with an
// unknown placeholder value in every instance of its module, so the next
// probe round evaluates past it and reveals what its coverage unblocks. It
// reports whether coverage grew.
func (an *analyzer) extendPlaceholders(placeholders map[string]cty.Value) bool {
	grew := false
	for _, src := range an.analysis.order {
		for _, modInst := range an.moduleInstancesOf(src.Module) {
			key := src.Resource.Instance(addrs.NoKey).Absolute(modInst).String()
			if _, ok := placeholders[key]; !ok {
				placeholders[key] = cty.DynamicVal
				grew = true
			}
		}
	}
	return grew
}

// moduleInstancesOf enumerates the instances of one module path, expanding
// for_each module calls through the same key computation resolution uses.
// A call whose keys cannot be computed contributes nothing; lint's
// child-module rule is what refuses that configuration, not this phase.
func (an *analyzer) moduleInstancesOf(module addrs.Module) []addrs.ModuleInstance {
	insts := []addrs.ModuleInstance{addrs.RootModuleInstance}
	node := an.cfg
	for _, name := range module {
		child, ok := node.Children[name]
		if !ok || child == nil || child.Module == nil {
			return nil
		}
		var keys []addrs.InstanceKey
		call := node.Module.ModuleCalls[name]
		if call != nil && call.ForEach != nil {
			var diag *hcl.Diagnostic
			keys, diag = identity.ChildModuleKeys(an.ctx, node.Module, fmt.Sprintf("module %q", name), call.ForEach)
			if diag != nil {
				return nil
			}
		} else {
			keys = []addrs.InstanceKey{addrs.NoKey}
		}
		next := make([]addrs.ModuleInstance, 0, len(insts)*len(keys))
		for _, inst := range insts {
			for _, key := range keys {
				next = append(next, inst.Child(name, key))
			}
		}
		insts = next
		node = child
	}
	return insts
}

// classify decides one demanded data source's eligibility, classifying its
// data dependencies first, and returns the stored record. Nil means the
// reference names no declared data source in that module, and the phase
// leaves it exactly as refused as it is today.
func (an *analyzer) classify(module addrs.Module, res addrs.Resource, neededBy string) *Source {
	key := sourceKey(module, res)
	if src, ok := an.analysis.sources[key]; ok {
		return src
	}
	if an.visiting[key] {
		// A cycle: the caller sees this transient record, refuses, and the
		// cycle's members each end up ineligible through propagation. The
		// record is not stored - the frame that opened this key finishes
		// its own classification and stores the real one.
		return &Source{
			Module: module, Resource: res, NeededBy: neededBy,
			ReasonSummary: SummaryNotReadable,
			ReasonDetail: fmt.Sprintf(
				"%s's value is needed to resolve the identity of %s, but it depends on itself through other data sources, and a cycle cannot be read in any order, so it cannot be read before the plan.",
				res.String(), neededBy),
		}
	}
	node := an.cfg.Descendent(module)
	if node == nil || node.Module == nil {
		return nil
	}
	rc := node.Module.DataResources[res.String()]
	if rc == nil {
		return nil
	}

	src := &Source{Module: module, Resource: res, Config: rc, NeededBy: neededBy}
	if configs.IsRemoteState(res.Type) {
		// Stage 3: terraform_remote_state goes through the same eligibility
		// pipeline a same-stack source does, below - credentials assumed
		// present per the ruling [Source.RemoteState] documents.
		src.RemoteState = true
	}
	if configs.IsTfeOutputs(res.Type) {
		// Stage 2: tfe_outputs goes through the same eligibility pipeline a
		// same-stack source does, below - credentials assumed present per
		// the ruling [Source.TfeOutputs] documents.
		src.TfeOutputs = true
	}

	an.visiting[key] = true
	defer delete(an.visiting, key)

	deps := make(map[string]SourceDep)
	refuse := func(clause string) {
		if src.ReasonSummary != "" {
			return // first reason wins; one sentence, not a pile
		}
		src.ReasonSummary = SummaryNotReadable
		src.ReasonDetail = fmt.Sprintf(
			"%s's value is needed to resolve the identity of %s, but %s, so it cannot be read before the plan.",
			res.String(), neededBy, clause)
	}

	// Rule 4, the depends_on half: naming a managed resource defers the
	// read until the dependency is planned, and there is nothing honest to
	// read before that. Naming another data source is an ordering edge.
	// depends_on's traversal is always resolved in this block's own module -
	// there is no module-crossing syntax for it - so every edge it
	// contributes is same-module.
	for _, trav := range rc.DependsOn {
		ref, refDiags := addrs.ParseRef(trav)
		if refDiags.HasErrors() {
			refuse(fmt.Sprintf("it carries a depends_on this phase cannot parse (%s)", refDiags.Err()))
			continue
		}
		switch {
		case ref == nil:
			continue
		default:
			if dep, ok := DataSubject(ref.Subject); ok {
				d := SourceDep{Module: module, Resource: dep}
				deps[d.key()] = d
				continue
			}
			refuse(fmt.Sprintf("it names %s in depends_on; in stock OpenTofu such a read is deferred until the dependency is planned or applied", ref.Subject.String()))
		}
	}

	// Rules 1 and 2: every expression - the block's own arguments plus its
	// count/for_each - must evaluate statically, with every same-stack data
	// source this evaluation can reach - this block's own module's, AND (by
	// #212) an ancestor's own, when a module-call variable carries the
	// reference there - standing in as coverage so that a reference to one
	// is an ordering edge rather than a failure. depModule is the
	// DECLARING module of the dependency, never assumed to be this block's
	// own - see [liveModuleEvaluator]'s doc for why a lookup scoped to one
	// module can never answer for another.
	record := func(depModule addrs.Module, dep addrs.Resource) {
		d := SourceDep{Module: depModule, Resource: dep}
		if d.key() == sourceKey(module, res) {
			return // this block's own reference to itself, not a dependency
		}
		deps[d.key()] = d
	}
	for _, ne := range an.sourceExpressions(rc) {
		errDetail, category, usedSelf, ok := an.evalRecorded(module, res, rc, ne, record)
		if ok {
			if usedSelf {
				src.PerInstance = true
			}
			continue
		}
		switch category {
		case configs.CategoryManagedResource:
			refuse(fmt.Sprintf("its %s depends on a managed resource: %s", ne.label, errDetail))
		case configs.CategoryRemoteState:
			refuse(fmt.Sprintf("its %s reads a cross-stack data source, which this stage does not read: %s", ne.label, errDetail))
		default:
			refuse(fmt.Sprintf("its %s is not statically evaluable: %s", ne.label, errDetail))
		}
	}

	// Rule 3: the provider must be configurable the way the projection
	// builder configures one - by statically evaluating its root provider
	// block. The exact line ConfiguredProvider already draws.
	//
	// Credential presence itself is not drawn here: the maintainer's ruling
	// on #181 moves that check to read time for both cross-stack flavors
	// (tfeAuthAvailable, consulted from [reader.readSource] in read.go, and
	// the builtin terraform provider's own backend-configure call for
	// terraform_remote_state), so that eligibility models what an owner
	// running this configuration actually has, the same treatment stage 1
	// gives the aws provider block.
	if src.ReasonSummary == "" {
		ok, detail, _ := an.providerConfigurable(node, rc)
		if !ok {
			src.ReasonSummary = SummaryProviderNotConfigurable
			src.ReasonDetail = fmt.Sprintf(
				"%s's value is needed to resolve the identity of %s, but %s.",
				res.String(), neededBy, detail)
		}
	}

	// Dependencies classify before this source finishes, which both orders
	// the read plan (a dependency lands in order first) and propagates a
	// dependency's own refusal.
	depKeys := make([]string, 0, len(deps))
	for k := range deps {
		depKeys = append(depKeys, k)
	}
	sort.Strings(depKeys)
	for _, dk := range depKeys {
		dep := deps[dk]
		src.Deps = append(src.Deps, dep)
		depSrc := an.classify(dep.Module, dep.Resource, res.String())
		switch {
		case depSrc == nil:
			refuse(fmt.Sprintf("it references %s, which %s does not declare", dep.Resource.String(), moduleLabel(dep.Module)))
		case depSrc.ReasonSummary != "":
			refuse(fmt.Sprintf("it depends on %s, which cannot be read before the plan (%s)", dep.Resource.String(), depSrc.ReasonDetail))
		}
	}

	src.Eligible = src.ReasonSummary == ""
	an.analysis.sources[key] = src
	an.analysis.order = append(an.analysis.order, src)
	return src
}

// namedExpr is one expression of a data block, with the label its refusal
// sentence names it by.
type namedExpr struct {
	label string
	expr  hcl.Expression

	// dynamicIterators names every "dynamic" block's own iterator that
	// legitimately encloses this expression - "statement" for
	// `dynamic "statement" { content { ... } }`, or whatever
	// `iterator = foo` names instead - innermost and outermost alike, the
	// same stacking a nested `dynamic "condition"` inside a
	// `dynamic "statement"` gets from HCL's own dynblock extension
	// (github.com/hashicorp/hcl/v2/ext/dynblock) and stock OpenTofu's
	// plan-time evaluator (internal/lang/eval.go's Scope.ExpandBlock).
	// [analyzer.evalRecorded] binds each one to an unknown key/value
	// placeholder, the same shape [selfRepetitionRoots] gives each/count,
	// rather than refusing it as an undeclared reference (issue #212).
	dynamicIterators map[string]bool
}

// sourceExpressions collects a data block's evaluable expressions: its
// count/for_each and every argument in its body. A body this phase cannot
// enumerate (anything but native syntax) yields a sentinel expression-less
// entry the caller refuses on, because "cannot analyze" must never read as
// "eligible".
func (an *analyzer) sourceExpressions(rc *configs.Resource) []namedExpr {
	var out []namedExpr
	if rc.Count != nil {
		out = append(out, namedExpr{label: "count", expr: rc.Count})
	}
	if rc.ForEach != nil {
		out = append(out, namedExpr{label: "for_each", expr: rc.ForEach})
	}
	body, ok := rc.Config.(*hclsyntax.Body)
	if !ok {
		out = append(out, namedExpr{label: "arguments"})
		return out
	}
	collectBodyExpressions(body, "argument", &out)
	return out
}

// metaArguments are the block-level names configs decodes into fields of
// its own; they stay visible on the remain body's attribute map and must
// not be evaluated as data-source arguments (provider = aws.x is not an
// expression at all).
var metaArguments = map[string]bool{
	"count":      true,
	"for_each":   true,
	"provider":   true,
	"depends_on": true,
}

func collectBodyExpressions(body *hclsyntax.Body, label string, out *[]namedExpr) {
	collectBodyExpressionsBound(body, label, nil, out)
}

// collectBodyExpressionsBound is [collectBodyExpressions] with bound
// carried down: the set of "dynamic" block iterator names legitimately in
// scope for every expression collected at or under body, from every
// "dynamic" block already enclosing it. nil (the top-level call's value)
// means none - the overwhelmingly common case, and the only one before
// issue #212.
func collectBodyExpressionsBound(body *hclsyntax.Body, label string, bound map[string]bool, out *[]namedExpr) {
	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if metaArguments[name] {
			continue
		}
		*out = append(*out, namedExpr{
			label:            fmt.Sprintf("%s %q", label, name),
			expr:             body.Attributes[name].Expr,
			dynamicIterators: bound,
		})
	}
	for _, block := range body.Blocks {
		switch block.Type {
		case "lifecycle":
			continue
		case "dynamic":
			collectDynamicBlockExpressions(block, label, bound, out)
		default:
			collectBodyExpressionsBound(block.Body, fmt.Sprintf("%s block's %s", block.Type, label), bound, out)
		}
	}
}

// collectDynamicBlockExpressions models a `dynamic "NAME" { ... }` block the
// way HCL's own dynblock extension (github.com/hashicorp/hcl/v2/ext/dynblock)
// and stock OpenTofu's plan-time evaluator both do (internal/lang/eval.go's
// Scope.ExpandBlock): for_each and labels evaluate in the ENCLOSING scope,
// with whatever iterators already enclose this dynamic block bound exactly
// like any other argument there; the block's own iterator - NAME, or
// whatever `iterator = foo` names instead - is a NEW binding that applies
// only inside "content", stacked on top of (never replacing) the iterators
// already bound there, so a nested dynamic block's content can still reach
// an outer one's iterator. Before this, the block's own for_each was
// silently skipped (its name, "for_each", collides with [metaArguments],
// meant for the enclosing block's OWN repetition meta-argument, never
// checked for a nested "dynamic" block at all) and "content" was walked as
// an ordinary nested block, so a reference like statement.value evaluated
// as an undefined variable rather than the iterator it names (issue #212;
// concretely, terraform-aws-modules/iam's aws_iam_policy_document data
// sources, which nest a `dynamic "condition"` inside a
// `dynamic "statement"` and read the outer iterator from the inner
// block's own content).
func collectDynamicBlockExpressions(block *hclsyntax.Block, label string, bound map[string]bool, out *[]namedExpr) {
	if len(block.Labels) == 0 {
		// A "dynamic" block with no label is invalid - OpenTofu's own schema
		// decode refuses it long before this phase ever sees the body - but
		// "cannot analyze" must never silently read as "eligible" (the same
		// rule [sourceExpressions]'s own sentinel enforces at the top
		// level), so an expression-less sentinel refuses rather than this
		// block's content simply vanishing from the walk.
		*out = append(*out, namedExpr{label: fmt.Sprintf("%s dynamic block with no label", label)})
		return
	}
	dynLabel := fmt.Sprintf("%s dynamic %q's", label, block.Labels[0])
	body := block.Body

	iterName := block.Labels[0]
	if attr, ok := body.Attributes["iterator"]; ok {
		if trav, travDiags := hcl.AbsTraversalForExpr(attr.Expr); !travDiags.HasErrors() && len(trav) > 0 {
			iterName = trav.RootName()
		}
	}

	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		if name == "iterator" {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		*out = append(*out, namedExpr{
			label:            fmt.Sprintf("%s %s", dynLabel, name),
			expr:             body.Attributes[name].Expr,
			dynamicIterators: bound,
		})
	}

	var contentBody *hclsyntax.Body
	for _, inner := range body.Blocks {
		if inner.Type == "content" {
			contentBody = inner.Body
		}
	}
	if contentBody == nil {
		// A "dynamic" block with no "content" sub-block is invalid for the
		// identical reason a missing label is - refuse rather than let this
		// block's (nonexistent) content read as trivially eligible.
		*out = append(*out, namedExpr{label: fmt.Sprintf("%s block with no content block", dynLabel)})
		return
	}
	childBound := make(map[string]bool, len(bound)+1)
	for k := range bound {
		childBound[k] = true
	}
	childBound[iterName] = true
	collectBodyExpressionsBound(contentBody, fmt.Sprintf("%s content", dynLabel), childBound, out)
}

// selfRepetitionRoots names the traversal roots this data block's own
// count/for_each meta-argument makes legitimately self-referential in one
// of the block's OTHER expressions: "count" when the block sets count,
// "each" when it sets for_each - the same per-block scoping stock OpenTofu
// gives every resource and data block (a nested block never inherits an
// ancestor's each/count; only its own). It is never applied to the
// count/for_each expression itself: that expression defines the repetition
// and cannot reference it, in this fork or in stock OpenTofu.
func selfRepetitionRoots(rc *configs.Resource, label string) map[string]bool {
	if label == "count" || label == "for_each" {
		return nil
	}
	roots := make(map[string]bool, 2)
	if rc.Count != nil {
		roots["count"] = true
	}
	if rc.ForEach != nil {
		roots["each"] = true
	}
	return roots
}

// evalRecorded evaluates one expression through the module's own static
// evaluator, with every declared same-stack data source of the module
// covered by an unknown placeholder whose consultation is recorded as an
// ordering edge, and this block's own count.index / each.key / each.value -
// when the expression is not the count/for_each expression itself - treated
// as a legitimate per-instance reference rather than a dynamic-value
// refusal: [Read] binds the real value once instance expansion is known,
// the same way stock OpenTofu's plan-time evaluator does
// (internal/tofu/evaluate.go, evaluationStateData.GetCountAttr /
// GetForEachAttr, populated per instance from InstanceKeyData).
//
// ok reports success; otherwise errDetail carries the evaluator's first
// error and category the refused reference's category when the error
// carries one. usedSelf reports whether the expression actually needed a
// self-repetition binding, so the caller can mark the source
// [Source.PerInstance] only when at least one argument genuinely varies per
// instance - the common case, where every instance shares one answer,
// keeps its existing one-call cost.
//
// module is the module DECLARING res - the block being classified, never
// assumed to be the same module as any dependency this evaluation reaches:
// [liveModuleEvaluator] builds this block's own module's evaluator, with
// its own data lookup, its own ancestor chain, and nothing borrowed from
// any other module's coverage (issue #212 - see that function's doc for
// the adversarial case this guards against).
func (an *analyzer) evalRecorded(module addrs.Module, res addrs.Resource, rc *configs.Resource, ne namedExpr, record func(addrs.Module, addrs.Resource)) (errDetail string, category configs.ReferenceCategory, usedSelf bool, ok bool) {
	if ne.expr == nil {
		// A namedExpr with no expression is always a sentinel: either the
		// whole block is not written in native syntax ([sourceExpressions]),
		// or a "dynamic" block this phase found was malformed in a way
		// OpenTofu's own schema decode would refuse anyway
		// ([collectDynamicBlockExpressions]). ne.label already names which.
		return fmt.Sprintf("%s cannot be analyzed", ne.label), configs.CategoryOther, false, false
	}
	defer func() {
		// The same guard identity's evalPure carries: static evaluation can
		// panic several layers down (a reference resolving back to a keyed
		// module call's own each.key), and one unevaluable expression is a
		// classification, not a crash.
		if rec := recover(); rec != nil {
			errDetail = fmt.Sprintf("evaluation failed: %v", rec)
			category = configs.CategoryOther
			usedSelf = false
			ok = false
		}
	}()

	eval := liveModuleEvaluator(an.ctx, an.cfg, module, an.lookupFactory(record))
	if eval == nil {
		return "its own module is no longer in the configuration tree; this is a defect in the calling code", configs.CategoryOther, false, false
	}
	ident := configs.StaticIdentifier{
		Module:    module,
		Subject:   fmt.Sprintf("%s's %s", res.String(), ne.label),
		DeclRange: ne.expr.Range(),
	}

	selfRoots := selfRepetitionRoots(rc, ne.label)
	var travs []hcl.Traversal
	for _, trav := range ne.expr.Variables() {
		root := trav.RootName()
		if selfRoots[root] {
			usedSelf = true
			continue
		}
		if ne.dynamicIterators[root] {
			continue
		}
		travs = append(travs, trav)
	}

	refs, refDiags := lang.References(addrs.ParseRef, travs)
	if refDiags.HasErrors() {
		detail, cat := firstHCLError(refDiags.ToHCL())
		return detail, cat, false, false
	}

	hclCtx, ctxDiags := eval.EvalContextWithParent(an.ctx, nil, ident, refs)
	if ctxDiags.HasErrors() {
		detail, cat := firstHCLError(ctxDiags)
		return detail, cat, false, false
	}
	if hclCtx == nil {
		hclCtx = &hcl.EvalContext{}
	}
	if len(selfRoots) > 0 || len(ne.dynamicIterators) > 0 {
		hclCtx = withPlaceholders(hclCtx, selfRoots, ne.dynamicIterators)
	}

	_, valDiags := ne.expr.Value(hclCtx)
	if valDiags.HasErrors() {
		detail, cat := firstHCLError(valDiags)
		return detail, cat, false, false
	}
	return "", "", usedSelf, true
}

// withPlaceholders returns a child of hclCtx with "each"/"count" bound to
// unknown placeholders exactly where selfRoots says the block's own
// count/for_each makes them legitimate, and every name in iterators (a
// "dynamic" block's own iterator - see [namedExpr.dynamicIterators]) bound
// the same key/value shape each gets, since HCL's dynblock extension gives
// a dynamic block's iterator the identical object shape as each. Analysis
// only needs to know whether the rest of the expression validates - the
// concrete per-instance value is bound later, in [reader], once an actual
// instance key (and, for a dynamic block, an actual iteration) is in hand.
func withPlaceholders(hclCtx *hcl.EvalContext, selfRoots, iterators map[string]bool) *hcl.EvalContext {
	child := hclCtx.NewChild()
	vars := make(map[string]cty.Value, len(selfRoots)+len(iterators))
	if selfRoots["count"] {
		vars["count"] = cty.ObjectVal(map[string]cty.Value{
			"index": cty.UnknownVal(cty.Number),
		})
	}
	if selfRoots["each"] {
		vars["each"] = cty.ObjectVal(map[string]cty.Value{
			"key":   cty.UnknownVal(cty.String),
			"value": cty.UnknownVal(cty.DynamicPseudoType),
		})
	}
	for name := range iterators {
		vars[name] = cty.ObjectVal(map[string]cty.Value{
			"key":   cty.UnknownVal(cty.String),
			"value": cty.UnknownVal(cty.DynamicPseudoType),
		})
	}
	child.Variables = vars
	return child
}

// firstHCLError extracts one error diagnostic's detail and reference
// category the same way every raise site on this path already does: the
// first diagnostic carrying a [configs.RefusedReference] wins outright (its
// category names what was actually refused), and only when none does does
// the first error's plain detail stand in, categoryless.
func firstHCLError(diags hcl.Diagnostics) (detail string, category configs.ReferenceCategory) {
	var fallback string
	for _, d := range diags {
		if d.Severity != hcl.DiagError {
			continue
		}
		dDetail := d.Detail
		if dDetail == "" {
			dDetail = d.Summary
		}
		if ref, isRef := d.Extra.(configs.RefusedReference); isRef {
			return dDetail, ref.Category
		}
		if fallback == "" {
			fallback = dDetail
		}
	}
	if fallback == "" {
		fallback = diags.Error()
	}
	return fallback, configs.CategoryOther
}

// findProviderConfig locates the root-module provider block a data source
// resolves to, the same lookup [providerConfigurable] and the tfe
// auth-surface rule both need. A nil result with no alias means the default
// configuration, which configures itself from the process environment.
func (an *analyzer) findProviderConfig(node *configs.Config, rc *configs.Resource) (pcAddr addrs.LocalProviderConfig, found *configs.Provider) {
	pcAddr = rc.ProviderConfigAddr()
	fqn := node.Module.ProviderForLocalConfig(pcAddr)

	root := an.cfg.Module
	keys := make([]string, 0, len(root.ProviderConfigs))
	for k := range root.ProviderConfigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		pc := root.ProviderConfigs[k]
		if pc.Alias != pcAddr.Alias {
			continue
		}
		if root.ProviderForLocalConfig(addrs.LocalProviderConfig{LocalName: pc.Name}) != fqn {
			continue
		}
		found = pc
		break
	}
	return pcAddr, found
}

// providerConfigurable draws eligibility rule 3: the provider configuration
// the data source resolves to must be statically evaluable from the root
// module's provider block, which is exactly what ConfiguredProvider will do
// at read time. An absent block for the default configuration is fine - the
// provider configures itself from the process environment - while an absent
// block for an aliased configuration is not. The found provider block (nil
// for the default configuration) is returned too, for [reader.readSource] in
// read.go to reuse when it draws tfe_outputs' read-time auth check.
func (an *analyzer) providerConfigurable(node *configs.Config, rc *configs.Resource) (bool, string, *configs.Provider) {
	pcAddr, found := an.findProviderConfig(node, rc)
	if found == nil {
		if pcAddr.Alias == "" {
			return true, "", nil
		}
		return false, fmt.Sprintf("the provider configuration %q it needs is not declared in the root module", pcAddr.StringCompact()), nil
	}

	body, ok := found.Config.(*hclsyntax.Body)
	if !ok {
		// A non-native provider block cannot be pre-checked; the configure
		// call at read time is the judge, and its refusal quotes the real
		// error. Analysis stays permissive rather than refusing working
		// configurations it merely cannot enumerate.
		return true, "", found
	}
	var exprs []namedExpr
	collectProviderExpressions(body, &exprs)
	for _, ne := range exprs {
		if errDetail, _, ok := an.evalProviderExpr(found, ne); !ok {
			return false, fmt.Sprintf(
				"its provider configuration provider.%s needs a value that cannot be evaluated before the plan: %s",
				providerDisplayName(found), errDetail), nil
		}
	}
	return true, "", found
}

func providerDisplayName(pc *configs.Provider) string {
	if pc.Alias != "" {
		return pc.Name + "." + pc.Alias
	}
	return pc.Name
}

// collectProviderExpressions walks a provider block's arguments and nested
// blocks. alias and version are the block's own meta-arguments and are
// skipped.
func collectProviderExpressions(body *hclsyntax.Body, out *[]namedExpr) {
	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if name == "alias" || name == "version" {
			continue
		}
		*out = append(*out, namedExpr{label: fmt.Sprintf("argument %q", name), expr: body.Attributes[name].Expr})
	}
	for _, block := range body.Blocks {
		collectProviderExpressions(block.Body, out)
	}
}

// evalProviderExpr evaluates one provider-block expression through the root
// module's static evaluator, with no data coverage: the line
// ConfiguredProvider draws is static evaluation alone, and a provider block
// reading a data source is exactly what rule 3 refuses.
func (an *analyzer) evalProviderExpr(pc *configs.Provider, ne namedExpr) (errDetail string, category configs.ReferenceCategory, ok bool) {
	defer func() {
		if rec := recover(); rec != nil {
			errDetail = fmt.Sprintf("evaluation failed: %v", rec)
			category = configs.CategoryOther
			ok = false
		}
	}()
	ident := configs.StaticIdentifier{
		Module:    addrs.RootModule,
		Subject:   fmt.Sprintf("provider.%s %s", providerDisplayName(pc), ne.label),
		DeclRange: ne.expr.Range(),
	}
	_, hclDiags := an.cfg.Module.StaticEvaluator.Pure().Evaluate(an.ctx, ne.expr, ident)
	if !hclDiags.HasErrors() {
		return "", "", true
	}
	for _, d := range hclDiags {
		if d.Severity != hcl.DiagError {
			continue
		}
		detail := d.Detail
		if detail == "" {
			detail = d.Summary
		}
		return detail, configs.CategoryOther, false
	}
	return hclDiags.Error(), configs.CategoryOther, false
}
