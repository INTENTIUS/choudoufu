// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0

package dataread

// Issue #193's fix class (c): a managed resource attribute reference
// answered from the resource block's own configuration argument - offline,
// from configuration alone, with no live read and no state.
//
// The rule is narrow and structural. aws_mq_broker.x.subnet_ids is refused
// as a managed reference by plain static evaluation, but subnet_ids is an
// argument the broker's own block SETS; the value is in the configuration,
// and reading it there is the same epistemic step this fork already takes
// when it synthesizes a resource's identity from its own arguments, taken
// through one more reference. aws_vpc.this[0].id is a different thing
// entirely: nothing in the configuration sets it, so there is nothing to
// project and it keeps refusing exactly as before. Which of the two a
// reference is, is decided by whether the block's body carries an attribute
// of that name - never by a type name, and never by a guess.
//
// # Two modes, one rule
//
// [Analyze] runs offline and needs COVERAGE, not values: it must decide
// whether a data source will be readable without contacting anything, which
// is what keeps tools/corpus-gen generable over 250 third-party
// configurations with no AWS account behind them. It projects with
// materialize false, so every projectable argument's value is cty.DynamicVal.
//
// [Read] runs against configured providers and needs the VALUE, because that
// value becomes a data source's argument and then a marker. It projects with
// materialize true, so every projectable argument carries what its expression
// actually evaluates to, with the data sources this phase has already read in
// scope.
//
// The two share this file so that the question "is this projectable" can only
// have one answer. Before the read side existed the projection had to default
// off, because a classification that says "eligible" for a plan that then
// refuses is the false-reassurance shape; with both halves here it is on.
//
// # What is deliberately not projected
//
//   - Nested blocks. A managed block's `user { ... }` is not an attribute, and
//     the cty shape it takes - a single object, a list, a set - is decided by
//     the provider's schema NestingMode, not by how many times it appears in
//     the body. Guessing from the syntax would be a cardinality guess, which
//     doc.go rules out. A reference to one refuses at coverage, cleanly.
//   - Whole-object uses. The projection carries what the body sets and
//     nothing else, so `jsonencode(aws_instance.web)` would otherwise hand
//     back a truncated object as if it were the whole - a wrong value rather
//     than a refusal. [unprojectedAttr] is what stops that: every projected
//     object carries one unknown attribute under a name no HCL traversal can
//     spell, so any use of the object AS A VALUE is unknown and refuses at
//     the same IsWhollyKnown guard an impure function refuses at, while
//     naming one of the block's own arguments is untouched.
//
//     The refusal could not be drawn at coverage instead. A traversal that
//     stops before naming an attribute is exactly what
//     `aws_instance.fleet[count.index].subnet_id` looks like to
//     [configs.lookupCoversTraversal] - a dynamic index is not part of the
//     traversal at all - so refusing on that shape would refuse the
//     commonest legitimate reference there is. What remains unguarded is
//     length() or keys() over a projected object, which answer about the
//     arguments the block writes rather than about the resource; nothing in
//     the corpus does that, and it is recorded here rather than assumed
//     away.
//   - An argument whose expression does not evaluate statically. It is absent
//     from the projected object, so a reference to it refuses at coverage in
//     the same words a provider-assigned attribute does.
//
// Expansion IS modelled: a count-expanded block projects as a tuple and a
// for_each-expanded one as an object keyed by instance key, the shape
// [configs.StaticDataLookup] documents, with that instance's own
// count.index/each.key/each.value bound while its arguments evaluate. That is
// not a refinement for its own sake - without it a splat over an expanded
// block would silently yield a one-element result, which is a wrong value
// rather than a refusal.

import (
	"context"
	"sort"

	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/zclconf/go-cty/cty"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/instances"
)

// unprojectedAttr names the one attribute every projected object carries
// beyond the block's own arguments: an unknown standing for everything the
// projection does not know, which is the provider's whole assignment half.
//
// It is what makes a whole-object use refuse instead of answering about a
// truncated object - an object with any unknown in it is not wholly known,
// and both [reader.decodeConfig] and [reader.decodeConfigForInstance]
// already refuse on exactly that. The slash makes it unspellable as an HCL
// traversal step, so no reference can name it and
// [configs.lookupCoversTraversal] can never report it as covering anything.
const unprojectedAttr = "//unprojected"

// managedProjector projects managed resource blocks' own arguments. One
// instance serves a whole [Analyze] or [Read] run, memoizing what it has
// already worked out.
type managedProjector struct {
	ctx context.Context
	cfg *configs.Config

	// materialize distinguishes the two modes this file's doc comment
	// describes: false yields cty.DynamicVal per projectable argument (the
	// offline classification, which needs coverage), true yields the value
	// the argument's expression evaluates to (the read phase, which needs
	// the value).
	materialize bool

	visiting map[string]bool
	cache    map[string]managedProj
}

// managedProj is one memoized projection: the value, whether there was one,
// and the data sources the projection's own expressions reached.
//
// deps is not bookkeeping. [analyzer.classify] learns a data source's
// dependencies by watching which addresses the evaluation asks its lookup
// for, so a projection served from the cache would record nothing and the
// second data source referencing the same managed block would end up
// ordered before a dependency it genuinely has. Replaying deps through the
// caller's own lookup on a cache hit is what keeps the read order
// well-founded.
type managedProj struct {
	val  cty.Value
	ok   bool
	deps []SourceDep
}

func newManagedProjector(ctx context.Context, cfg *configs.Config, materialize bool) *managedProjector {
	return &managedProjector{
		ctx:         ctx,
		cfg:         cfg,
		materialize: materialize,
		visiting:    make(map[string]bool),
		cache:       make(map[string]managedProj),
	}
}

// project answers a managed resource reference with the block's own
// arguments, or reports that it has nothing to say. lookup is the caller's
// own per-module data-source coverage factory: the projected block's
// expressions resolve their own data references through it, so an ancestor's
// reads are visible exactly where they should be and nowhere else (see
// [liveModuleEvaluator]).
func (p *managedProjector) project(module addrs.Module, res addrs.Resource, lookup func(addrs.Module) configs.StaticDataLookup) (cty.Value, bool) {
	if p == nil || res.Mode != addrs.ManagedResourceMode {
		return cty.NilVal, false
	}
	key := sourceKey(module, res)
	if memo, hit := p.cache[key]; hit {
		p.replay(memo.deps, lookup)
		return memo.val, memo.ok
	}
	if p.visiting[key] {
		// A cycle between managed blocks: nothing to project. Not memoized -
		// the frame that opened this key finishes and stores the real answer.
		return cty.NilVal, false
	}
	node := p.cfg.Descendent(module)
	if node == nil || node.Module == nil {
		return cty.NilVal, false
	}
	rc := node.Module.ManagedResources[res.String()]
	if rc == nil {
		return cty.NilVal, false
	}
	body, native := rc.Config.(*hclsyntax.Body)
	if !native {
		// A body this phase cannot enumerate (JSON syntax). "Cannot analyze"
		// must never read as "projectable".
		return cty.NilVal, false
	}

	p.visiting[key] = true
	observed := make(map[string]SourceDep)
	val, ok := p.build(module, res, rc, body, p.watch(lookup, observed))
	delete(p.visiting, key)

	deps := make([]SourceDep, 0, len(observed))
	for _, d := range observed {
		deps = append(deps, d)
	}
	sort.Slice(deps, func(i, j int) bool { return deps[i].key() < deps[j].key() })

	if ok || !p.materialize {
		// A negative answer is memoized only in the offline mode, where the
		// world the projection reads cannot change during the run. [Read]'s
		// own lookup grows as each source is read, so a projection that had
		// nothing to say early in the run may have something to say later,
		// and caching the earlier "no" would refuse a value that is now
		// available.
		p.cache[key] = managedProj{val: val, ok: ok, deps: deps}
	}
	return val, ok
}

// watch wraps the caller's lookup factory so that every data source the
// projection's own expressions reach is recorded, for [managedProj.deps].
func (p *managedProjector) watch(lookup func(addrs.Module) configs.StaticDataLookup, observed map[string]SourceDep) func(addrs.Module) configs.StaticDataLookup {
	return func(m addrs.Module) configs.StaticDataLookup {
		inner := lookup(m)
		return func(addr addrs.Resource) (cty.Value, bool) {
			val, ok := inner(addr)
			if ok && addr.Mode == addrs.DataResourceMode {
				d := SourceDep{Module: m, Resource: addr}
				observed[d.key()] = d
			}
			return val, ok
		}
	}
}

// replay asks the caller's own lookup for every data source a memoized
// projection reached, so that a cache hit records the same dependency edges
// a fresh computation would have. See [managedProj].
func (p *managedProjector) replay(deps []SourceDep, lookup func(addrs.Module) configs.StaticDataLookup) {
	for _, d := range deps {
		lookup(d.Module)(d.Resource)
	}
}

// build does the projection proper: expansion first, then one object per
// instance carrying the arguments that evaluate, then the aggregate shape a
// whole-resource reference evaluates to.
func (p *managedProjector) build(module addrs.Module, res addrs.Resource, rc *configs.Resource, body *hclsyntax.Body, lookup func(addrs.Module) configs.StaticDataLookup) (cty.Value, bool) {
	eval := liveModuleEvaluator(p.ctx, p.cfg, module, lookup)
	if eval == nil {
		return cty.NilVal, false
	}

	keys, eachVals, _, ok := staticExpansion(p.ctx, module, res.String(), rc.Count, rc.ForEach, eval)
	if !ok {
		// A block whose own instance keys are not knowable before the plan
		// has no aggregate shape, and inventing one is how a splat over it
		// returns one element where the configuration has several.
		return cty.NilVal, false
	}
	if len(keys) == 0 {
		if rc.ForEach != nil {
			return cty.EmptyObjectVal, true
		}
		return cty.EmptyTupleVal, true
	}

	names := make([]string, 0, len(body.Attributes))
	for name := range body.Attributes {
		if metaArguments[name] {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)

	perInstance := make([]map[string]cty.Value, len(keys))
	common := make(map[string]bool, len(names))
	for i, key := range keys {
		instEval := eval.WithRepetitionData(repetitionFor(key, eachVals))
		attrs := make(map[string]cty.Value, len(names))
		for _, name := range names {
			val, argOK := p.argument(module, res, name, body.Attributes[name].Expr, instEval)
			if !argOK {
				continue
			}
			attrs[name] = val
		}
		perInstance[i] = attrs
		if i == 0 {
			for name := range attrs {
				common[name] = true
			}
			continue
		}
		for name := range common {
			if _, present := attrs[name]; !present {
				delete(common, name)
			}
		}
	}
	if len(common) == 0 {
		return cty.NilVal, false
	}

	objs := make([]cty.Value, len(keys))
	for i, attrs := range perInstance {
		obj := make(map[string]cty.Value, len(common)+1)
		for name := range common {
			obj[name] = attrs[name]
		}
		obj[unprojectedAttr] = cty.UnknownVal(cty.String)
		objs[i] = cty.ObjectVal(obj)
	}

	switch {
	case rc.ForEach != nil:
		byKey := make(map[string]cty.Value, len(keys))
		for i, key := range keys {
			sk, isString := key.(addrs.StringKey)
			if !isString {
				return cty.NilVal, false
			}
			byKey[string(sk)] = objs[i]
		}
		return cty.ObjectVal(byKey), true
	case rc.Count != nil:
		return cty.TupleVal(objs), true
	default:
		return objs[0], true
	}
}

// argument evaluates one of a managed block's own attributes for one
// instance, and turns it into whatever this projector's mode wants: coverage
// (cty.DynamicVal) offline, the real value for the read phase.
func (p *managedProjector) argument(module addrs.Module, res addrs.Resource, name string, expr hclsyntax.Expression, eval *configs.StaticEvaluator) (cty.Value, bool) {
	val, _, ok := staticEvalExpr(p.ctx, module, res.String(), "argument "+name, expr, eval)
	if !ok {
		return cty.NilVal, false
	}
	if !p.materialize {
		return cty.DynamicVal, true
	}
	// IsWhollyKnown handles a marked value (it is one of the three guards
	// internal/live/marksafe's doc names as mark-safe), and an unknown here
	// means the expression reached something only the plan settles - which is
	// exactly what must not become a data source's argument.
	if !val.IsWhollyKnown() {
		return cty.NilVal, false
	}
	return val, true
}

// repetitionFor binds one instance key's own count.index or
// each.key/each.value, the same binding identity resolution gives a
// per-instance evaluation. A key shape that carries no repetition value -
// there is none for an unexpanded block - binds nothing, which leaves any
// count/each reference refusing exactly as it does with no repetition data
// at all.
func repetitionFor(key addrs.InstanceKey, eachVals map[string]cty.Value) instances.RepetitionData {
	switch k := key.(type) {
	case addrs.IntKey:
		return instances.RepetitionData{CountIndex: cty.NumberIntVal(int64(k))}
	case addrs.StringKey:
		rd := instances.RepetitionData{EachKey: cty.StringVal(string(k))}
		if val, ok := eachVals[string(k)]; ok {
			rd.EachValue = val
		}
		return rd
	}
	return instances.RepetitionData{}
}

// lookupFactory is the data-source coverage closure factory lifted out of
// [analyzer.evalRecorded] so the projection can reuse it, with the
// managed-mode branch added (#193).
func (an *analyzer) lookupFactory(record func(addrs.Module, addrs.Resource)) func(addrs.Module) configs.StaticDataLookup {
	return func(m addrs.Module) configs.StaticDataLookup {
		return func(addr addrs.Resource) (cty.Value, bool) {
			depNode := an.cfg.Descendent(m)
			if depNode == nil || depNode.Module == nil {
				return cty.NilVal, false
			}
			dep := depNode.Module.DataResources[addr.String()]
			if dep == nil {
				if addr.Mode == addrs.ManagedResourceMode {
					return an.proj.project(m, addr, an.lookupFactory(record))
				}
				return cty.NilVal, false
			}
			record(m, addr)
			return cty.DynamicVal, true
		}
	}
}
