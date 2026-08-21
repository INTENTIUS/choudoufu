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

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/live/lint"
)

// This file is the phase's SECOND demand class, GitHub issue #349's
// sub-problem 2: data sources a ROOT OUTPUT's value reaches, rather than
// data sources an identity reaches.
//
// The two classes share every offline eligibility rule (analyze.go's
// [analyzer.classify]) and the whole read machinery (read.go), and differ in
// exactly two ways, both of which are the reason this is a separate entry
// point rather than a parameter:
//
//   - Demand is derived by READING the output expressions, not by probing
//     identity resolution. Resolution never asks for these sources, so no
//     probe round ever names them.
//
//   - A source this class cannot read is SCOPED, never fatal. Identity's
//     contract is the opposite and stays that way (see [Read]): a hole in
//     the identity map plans to create objects that already exist, so it
//     must stop the run. A hole here costs one root output its prior value,
//     which renders as "+ name = ..." in the plan - the honest gap #349
//     describes, and strictly better than refusing the estate. Widening
//     demand under identity's fatal contract would have turned "one output
//     shows +" into "live-plan refuses the whole estate", which is the
//     parity regression #349's own scoping named.

// LiveProviders returns the providers this configuration manages real remote
// objects through, and it is the structural boundary the root-output read
// class stays inside. A data source whose provider is not in this set is
// never read by [AnalyzeRootOutputs] and, since the command layer hands the
// read phase a seam built from this same set, could not be read even if it
// were.
//
// # What the set means, and why it is not a provider list
//
// The rule the boundary states is "this run is already reading the live
// system through this provider, so one more read of the same kind is not a
// new class of side effect." A pre-plan phase that only ever issues
// read-only calls to the same remote APIs the projection is already reading
// keeps live-plan a pure preview of the world. A phase that could reach a
// provider whose read RUNS something locally would not, and
// data "external" - whose whole contract is executing a program named by its
// own arguments - is the shape that makes the difference concrete.
//
// So the set is derived, per run, from two measurements this repository
// already keeps, and never from a list of provider names:
//
//  1. The providers this configuration's own MANAGED resources use. A
//     provider with no managed resource type in the configuration is not one
//     this estate owns objects through. hashicorp/external is excluded here
//     for every configuration that can ever be written, because the provider
//     serves no managed resource type at all: it is a data source and
//     nothing else.
//
//  2. Minus the providers whose types are LOGICAL - the store-only and
//     local-effect families internal/live/lint classifies off
//     live/logical-schemas.json (hashicorp/random, /null, /time, /tls,
//     /local and the builtin terraform provider). Their resources have no
//     remote object behind them at all, so a run is not "already reading the
//     live system" through one, and their data sources read the local
//     machine rather than an API.
//
// Both halves are generated measurements rather than judgments typed here:
// (1) is read off the configuration, and (2) off [lint.ClassifyLogicalType],
// whose table tools/row-gen derives from provider schemas. The rule reaches
// every data source of every cloud provider an estate is built on - for the
// aws provider alone that is several hundred data source types, not the
// three that found it - and a future cloud provider this fork supports is
// covered the day an estate declares a managed resource of it, with no edit
// here.
func LiveProviders(cfg *configs.Config) map[addrs.Provider]bool {
	live := make(map[addrs.Provider]bool)
	var walk func(node *configs.Config)
	walk = func(node *configs.Config) {
		if node == nil || node.Module == nil {
			return
		}
		for _, rc := range node.Module.ManagedResources {
			if _, logical := lint.ClassifyLogicalType(rc.Type); logical {
				// A logical type's prior state lives in a record, not in a
				// cloud. Nothing about this run is "already reading the live
				// system" through its provider.
				continue
			}
			live[node.Module.ProviderForLocalConfig(rc.ProviderConfigAddr())] = true
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(cfg)
	return live
}

// AnalyzeRootOutputs derives which data sources the configuration's
// root-level `output` blocks reach and classifies each one, offline, exactly
// as [Analyze] classifies an identity-demanded source - plus the boundary
// [LiveProviders] draws.
//
// The result is an [Analysis] like any other, so [ReadForOutputs] can read it
// with the same machinery, in the same dependency order. Nothing here is
// fatal and nothing here refuses a configuration: an ineligible source simply
// carries its reason and is skipped at read time.
func AnalyzeRootOutputs(ctx context.Context, cfg *configs.Config, opts Options) *Analysis {
	a := &Analysis{sources: make(map[string]*Source), projectManaged: !opts.SkipManagedProjection, scoped: true}
	if cfg == nil || cfg.Module == nil || cfg.Module.StaticEvaluator == nil || len(cfg.Module.Outputs) == 0 {
		return a
	}
	an := &analyzer{ctx: ctx, cfg: cfg, analysis: a, schemas: opts.Schemas, visiting: make(map[string]bool)}
	if a.projectManaged {
		an.proj = newManagedProjector(ctx, cfg, false)
	}

	for _, want := range rootOutputDataDemand(cfg) {
		if _, seen := a.sources[sourceKey(want.module, want.resource)]; seen {
			continue
		}
		an.classify(want.module, want.resource, want.neededBy)
	}
	// The boundary is applied over the finished order rather than per demand
	// root, because classify recurses: a source demanded only as another
	// source's dependency is stored by that recursion and never passes
	// through the loop above.
	live := LiveProviders(cfg)
	for _, src := range a.order {
		an.confineToLiveProviders(src, live)
	}
	return a
}

// confineToLiveProviders applies [LiveProviders]' boundary to one classified
// source, turning an otherwise-eligible source of a provider this estate does
// not manage objects through into an ineligible one. It never makes an
// ineligible source eligible.
func (an *analyzer) confineToLiveProviders(src *Source, live map[addrs.Provider]bool) {
	if src == nil || !src.Eligible {
		return
	}
	node := an.cfg.Descendent(src.Module)
	if node == nil || node.Module == nil {
		return
	}
	provider := node.Module.ProviderForLocalConfig(src.Config.ProviderConfigAddr())
	if live[provider] {
		return
	}
	src.Eligible = false
	src.ReasonSummary = SummaryProviderNotLive
	src.ReasonDetail = fmt.Sprintf(
		"%s's value would settle the root output %s, but %s manages no live object in this configuration, so this run is not already reading the live system through it and a pre-plan read of it would be a new kind of side effect rather than one more read of an API the projection already reads. The output keeps the value the plan itself computes for it.",
		src.Resource.String(), src.NeededBy, provider.String())
}

// outputDemand is one data source a root output's value reaches.
type outputDemand struct {
	module   addrs.Module
	resource addrs.Resource
	neededBy string
}

// rootOutputDataDemand walks every root-level output's value expression and
// returns every data resource it can reach, with the module that declares
// it. Deterministic: sorted by output name, then by the source's own key.
//
// The walk follows the four hops a value can take between an output and a
// data source, and no others:
//
//   - local.x, into that module's own local value expression;
//   - module.c.o, into child module c's own output expression;
//   - var.v inside a module, back out to the argument the calling block
//     passes for it, evaluated in the CALLER's module - which is how a data
//     source declared in one module reaches an output declared in another;
//   - data.t.n, which is the answer.
//
// A managed-resource reference is not followed: the projection already
// materialized what it knows about those, and what it does not know is not a
// data source's to supply. path.*, terraform.*, count/each and function calls
// carry no data reference of their own; whatever they are applied to is
// reached through its own traversal.
func rootOutputDataDemand(cfg *configs.Config) []outputDemand {
	names := make([]string, 0, len(cfg.Module.Outputs))
	for name := range cfg.Module.Outputs {
		names = append(names, name)
	}
	sort.Strings(names)

	var out []outputDemand
	for _, name := range names {
		w := &demandWalk{cfg: cfg, seen: make(map[string]bool)}
		w.expr(cfg, cfg.Module.Outputs[name].Expr, 0)
		found := w.found
		sort.Slice(found, func(i, j int) bool {
			return sourceKey(found[i].module, found[i].resource) < sourceKey(found[j].module, found[j].resource)
		})
		for _, f := range found {
			out = append(out, outputDemand{module: f.module, resource: f.resource, neededBy: fmt.Sprintf("the root output %q", name)})
		}
	}
	return out
}

// maxOutputDemandDepth bounds how deep the walk chases a chain of locals,
// module outputs and module-call arguments. It is the same defensive backstop
// [maxForEachAliasDepth] is, not a limit real configurations approach: a
// self-referential chain is a configuration OpenTofu itself refuses, and this
// walk must not hang on one before it gets there.
const maxOutputDemandDepth = 32

type demandWalk struct {
	cfg   *configs.Config
	seen  map[string]bool
	found []outputDemand
}

func (w *demandWalk) expr(node *configs.Config, expr hcl.Expression, depth int) {
	if expr == nil || node == nil || node.Module == nil || depth > maxOutputDemandDepth {
		return
	}
	for _, trav := range expr.Variables() {
		ref, diags := addrs.ParseRef(trav)
		if diags.HasErrors() || ref == nil {
			continue
		}
		w.subject(node, ref.Subject, depth)
	}
}

func (w *demandWalk) subject(node *configs.Config, subject addrs.Referenceable, depth int) {
	if res, ok := DataSubject(subject); ok {
		key := sourceKey(node.Path, res)
		if !w.seen[key] {
			w.seen[key] = true
			w.found = append(w.found, outputDemand{module: node.Path, resource: res})
		}
		return
	}
	switch s := subject.(type) {
	case addrs.LocalValue:
		local := node.Module.Locals[s.Name]
		if local == nil {
			return
		}
		key := node.Path.String() + "\x00local." + s.Name
		if w.seen[key] {
			return
		}
		w.seen[key] = true
		w.expr(node, local.Expr, depth+1)

	case addrs.ModuleCallInstanceOutput:
		w.moduleOutput(node, s.Call.Call.Name, s.Name, depth)
	case addrs.ModuleCallOutput:
		w.moduleOutput(node, s.Call.Name, s.Name, depth)

	case addrs.InputVariable:
		// A variable's value is an expression in the CALLER, so the walk
		// leaves this module and continues in the parent - the only hop that
		// moves up the tree rather than down it.
		parent := node.Parent
		if parent == nil || parent.Module == nil || len(node.Path) == 0 {
			return
		}
		call := parent.Module.ModuleCalls[node.Path[len(node.Path)-1]]
		if call == nil || call.Config == nil {
			return
		}
		key := node.Path.String() + "\x00var." + s.Name
		if w.seen[key] {
			return
		}
		w.seen[key] = true
		attrs, _ := call.Config.JustAttributes()
		attr := attrs[s.Name]
		if attr == nil {
			return
		}
		w.expr(parent, attr.Expr, depth+1)
	}
}

func (w *demandWalk) moduleOutput(node *configs.Config, callName, outputName string, depth int) {
	child := node.Children[callName]
	if child == nil || child.Module == nil {
		return
	}
	output := child.Module.Outputs[outputName]
	if output == nil {
		return
	}
	key := child.Path.String() + "\x00output." + outputName
	if w.seen[key] {
		return
	}
	w.seen[key] = true
	w.expr(child, output.Expr, depth+1)
}
