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

	// CrossStack marks the two cross-stack flavors (terraform_remote_state,
	// tfe_outputs). The phase does not cover them in this stage: they keep
	// exactly the refusals they have today, and this record only says the
	// demand exists.
	CrossStack bool

	// Eligible reports that the phase can read this source before the plan:
	// static arguments and count/for_each, a statically configurable
	// provider, and no managed-resource dependency.
	Eligible bool

	// ReasonSummary and ReasonDetail are the refusal for an ineligible
	// source: ReasonSummary is one of this package's Summary constants and
	// ReasonDetail the class-specific sentence.
	ReasonSummary string
	ReasonDetail  string

	// Deps are the same-module data sources this one references, directly,
	// through locals, or in depends_on - the edges the topological read
	// order follows. Sorted.
	Deps []addrs.Resource
}

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
	an := &analyzer{ctx: ctx, cfg: cfg, analysis: a, schemas: opts.Schemas, visiting: make(map[string]bool)}

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
		if ref.Category != configs.CategoryDataSource && ref.Category != configs.CategoryCrossStackDataSource {
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

// extendPlaceholders covers every classified same-stack source with an
// unknown placeholder value in every instance of its module, so the next
// probe round evaluates past it and reveals what its coverage unblocks. It
// reports whether coverage grew.
func (an *analyzer) extendPlaceholders(placeholders map[string]cty.Value) bool {
	grew := false
	for _, src := range an.analysis.order {
		if src.CrossStack {
			continue
		}
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
	if configs.IsCrossStackDataSource(res.Type) {
		src.CrossStack = true
		an.analysis.sources[key] = src
		an.analysis.order = append(an.analysis.order, src)
		return src
	}

	an.visiting[key] = true
	defer delete(an.visiting, key)

	deps := make(map[string]addrs.Resource)
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
				deps[dep.String()] = dep
				continue
			}
			refuse(fmt.Sprintf("it names %s in depends_on; in stock OpenTofu such a read is deferred until the dependency is planned or applied", ref.Subject.String()))
		}
	}

	// Rules 1 and 2: every expression - the block's own arguments plus its
	// count/for_each - must evaluate statically, with other same-stack data
	// sources standing in as coverage so that a reference to one is an
	// ordering edge rather than a failure.
	record := func(dep addrs.Resource) {
		if dep.String() != res.String() {
			deps[dep.String()] = dep
		}
	}
	for _, ne := range an.sourceExpressions(rc) {
		errDetail, category, ok := an.evalRecorded(node, module, res, ne, record)
		if ok {
			continue
		}
		switch category {
		case configs.CategoryManagedResource:
			refuse(fmt.Sprintf("its %s depends on a managed resource: %s", ne.label, errDetail))
		case configs.CategoryCrossStackDataSource:
			refuse(fmt.Sprintf("its %s reads a cross-stack data source, which this stage does not read: %s", ne.label, errDetail))
		default:
			refuse(fmt.Sprintf("its %s is not statically evaluable: %s", ne.label, errDetail))
		}
	}

	// Rule 3: the provider must be configurable the way the projection
	// builder configures one - by statically evaluating its root provider
	// block. The exact line ConfiguredProvider already draws.
	if src.ReasonSummary == "" {
		if ok, detail := an.providerConfigurable(node, rc); !ok {
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
		depSrc := an.classify(module, dep, res.String())
		switch {
		case depSrc == nil:
			refuse(fmt.Sprintf("it references %s, which this module does not declare", dep.String()))
		case depSrc.CrossStack:
			refuse(fmt.Sprintf("it depends on %s, a cross-stack data source this stage does not read", dep.String()))
		case depSrc.ReasonSummary != "":
			refuse(fmt.Sprintf("it depends on %s, which cannot be read before the plan (%s)", dep.String(), depSrc.ReasonDetail))
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
}

// sourceExpressions collects a data block's evaluable expressions: its
// count/for_each and every argument in its body. A body this phase cannot
// enumerate (anything but native syntax) yields a sentinel expression-less
// entry the caller refuses on, because "cannot analyze" must never read as
// "eligible".
func (an *analyzer) sourceExpressions(rc *configs.Resource) []namedExpr {
	var out []namedExpr
	if rc.Count != nil {
		out = append(out, namedExpr{"count", rc.Count})
	}
	if rc.ForEach != nil {
		out = append(out, namedExpr{"for_each", rc.ForEach})
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
	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if metaArguments[name] {
			continue
		}
		*out = append(*out, namedExpr{fmt.Sprintf("%s %q", label, name), body.Attributes[name].Expr})
	}
	for _, block := range body.Blocks {
		if block.Type == "lifecycle" {
			continue
		}
		collectBodyExpressions(block.Body, fmt.Sprintf("%s block's %s", block.Type, label), out)
	}
}

// evalRecorded evaluates one expression through the module's own static
// evaluator, with every declared same-stack data source of the module
// covered by an unknown placeholder whose consultation is recorded as an
// ordering edge. ok reports success; otherwise errDetail carries the
// evaluator's first error and category the refused reference's category
// when the error carries one.
func (an *analyzer) evalRecorded(node *configs.Config, module addrs.Module, res addrs.Resource, ne namedExpr, record func(addrs.Resource)) (errDetail string, category configs.ReferenceCategory, ok bool) {
	if ne.expr == nil {
		return "the block is not written in native syntax, and this phase enumerates arguments only there", configs.CategoryOther, false
	}
	defer func() {
		// The same guard identity's evalPure carries: static evaluation can
		// panic several layers down (a reference resolving back to a keyed
		// module call's own each.key), and one unevaluable expression is a
		// classification, not a crash.
		if rec := recover(); rec != nil {
			errDetail = fmt.Sprintf("evaluation failed: %v", rec)
			category = configs.CategoryOther
			ok = false
		}
	}()

	eval := node.Module.StaticEvaluator.Pure().WithDataResults(func(addr addrs.Resource) (cty.Value, bool) {
		dep := node.Module.DataResources[addr.String()]
		if dep == nil || configs.IsCrossStackDataSource(addr.Type) {
			return cty.NilVal, false
		}
		record(addr)
		return cty.DynamicVal, true
	})
	ident := configs.StaticIdentifier{
		Module:    module,
		Subject:   fmt.Sprintf("%s's %s", res.String(), ne.label),
		DeclRange: ne.expr.Range(),
	}
	_, hclDiags := eval.Evaluate(an.ctx, ne.expr, ident)
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
		if ref, isRef := d.Extra.(configs.RefusedReference); isRef {
			return detail, ref.Category, false
		}
		if errDetail == "" {
			errDetail = detail
		}
	}
	if errDetail == "" {
		errDetail = hclDiags.Error()
	}
	return errDetail, configs.CategoryOther, false
}

// providerConfigurable draws eligibility rule 3: the provider configuration
// the data source resolves to must be statically evaluable from the root
// module's provider block, which is exactly what ConfiguredProvider will do
// at read time. An absent block for the default configuration is fine - the
// provider configures itself from the process environment - while an absent
// block for an aliased configuration is not.
func (an *analyzer) providerConfigurable(node *configs.Config, rc *configs.Resource) (bool, string) {
	pcAddr := rc.ProviderConfigAddr()
	fqn := node.Module.ProviderForLocalConfig(pcAddr)

	root := an.cfg.Module
	keys := make([]string, 0, len(root.ProviderConfigs))
	for k := range root.ProviderConfigs {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var found *configs.Provider
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
	if found == nil {
		if pcAddr.Alias == "" {
			return true, ""
		}
		return false, fmt.Sprintf("the provider configuration %q it needs is not declared in the root module", pcAddr.StringCompact())
	}

	body, ok := found.Config.(*hclsyntax.Body)
	if !ok {
		// A non-native provider block cannot be pre-checked; the configure
		// call at read time is the judge, and its refusal quotes the real
		// error. Analysis stays permissive rather than refusing working
		// configurations it merely cannot enumerate.
		return true, ""
	}
	var exprs []namedExpr
	collectProviderExpressions(body, &exprs)
	for _, ne := range exprs {
		if errDetail, _, ok := an.evalProviderExpr(found, ne); !ok {
			return false, fmt.Sprintf(
				"its provider configuration provider.%s needs a value that cannot be evaluated before the plan: %s",
				providerDisplayName(found), errDetail)
		}
	}
	return true, ""
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
		*out = append(*out, namedExpr{fmt.Sprintf("argument %q", name), body.Attributes[name].Expr})
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
