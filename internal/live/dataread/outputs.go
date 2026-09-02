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

// The phase's provider boundary has TWO tiers, and which one applies is the
// only thing the two demand classes disagree about. Both are derived per run;
// neither is a list of provider names.
//
//	tier 1, [Boundary.servesLiveObjects] - the provider's own schema declares
//	        at least one non-logical MANAGED resource type. A provider that
//	        serves data sources and nothing else is not an infrastructure
//	        provider, and hashicorp/external - whose whole contract is running
//	        a program named by its own arguments - is exactly that shape.
//	        Applies to BOTH classes, and for identity demand it refuses the
//	        run.
//
//	tier 2, [LiveProviders] - the provider manages a live object in THIS
//	        configuration. Strictly narrower. Applies to the root-output class
//	        only, where an excluded source costs one output its prior value
//	        and nothing else, so the stricter line is free.
//
// # Why the tiers, rather than one line for both
//
// The root-output class shipped with tier 2 and the identity class shipped
// with no boundary at all - an adversarial audit on 2026-08-21 found the
// older, wider-reaching path (#179) completely unconfined, so an ordinary
// configuration could get data "external" run during a live-plan by putting
// its result in an identity-bearing position.
//
// The obvious fix, applying tier 2 to both, was measured and rejected: it
// refuses every configuration whose identity reads a data source of a
// provider it manages nothing through - data.cloudflare_zone in an
// aws-managed estate, and this package's own fixtures - none of which can run
// anything locally, and all of which stock OpenTofu plans without complaint.
// HANDOFF.md's "parity is the bar" and its corollary that "refusing is not
// automatically the safe answer" both point the same way: the identity class
// gets the line that catches the hazard, not the line that was already
// written.
//
// # What tier 1 catches, and what it admits it does not
//
// The property that actually matters is "reading this could run something on
// the machine planning". Nothing in a provider schema states that, so tier 1
// uses the closest thing the schema does state: whether the provider is in
// the business of managing infrastructure at all. hashicorp/external and
// hashicorp/http declare no managed types and are excluded; the logical
// family (hashicorp/random, /null, /time, /tls, /local, the builtin terraform
// provider) declares only types [lint.ClassifyLogicalType] measures as
// logical, whose data sources read the local machine, and is excluded too.
// Every cloud provider an estate is actually built on is admitted, whether or
// not this particular configuration manages objects through it.
//
// It does not catch a provider that manages real infrastructure AND ships a
// data source with a local side effect. No derivation available here would,
// and stock OpenTofu reads that data source during its own plan, so this is
// parity rather than a hole this fork opens.
//
// # What the tier-2 set means, and why it is not a provider list
//
// The rule tier 2 states is "this run is already reading the live system
// through this provider, so one more read of the same kind is not a new class
// of side effect." A pre-plan phase that only ever issues read-only calls to
// the same remote APIs the projection is already reading keeps live-plan a
// pure preview of the world.
//
// So the set is derived, per run, from three measurements this repository
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
//  3. Intersected with what each provider's OWN SCHEMA declares, when the
//     caller can say (declared, below). (1) reads the provider off
//     [configs.Module.ProviderForLocalConfig], which answers with whatever
//     source address a `required_providers` entry bound the resource's local
//     name to - and nothing in that lookup checks that the provider actually
//     serves the type. So
//
//     required_providers { aws = { source = "hashicorp/external" } }
//     resource "aws_s3_bucket" "b" { ... }
//
//     votes hashicorp/external into the live set on the strength of a type
//     it does not serve, and every data source of the local-execution
//     provider becomes readable behind it. Requiring the provider's own
//     schema to declare the type closes that, and it is the provider's
//     measurement rather than ours.
//
// declared is which managed resource types each provider's schema declares,
// or nil. A provider ABSENT from it is not cross-checked at all: an absent
// entry means this run never got that provider's schema (the plugin would
// not start, or the caller had no schema source), which is the absence of
// evidence rather than evidence of absence, and refusing on it would turn
// every schema-less caller - live-check, every package-level test - into one
// that reads nothing. The command layer always supplies it.
//
// All three halves are generated measurements rather than judgments typed
// here: (1) is read off the configuration, (2) off
// [lint.ClassifyLogicalType], whose table tools/row-gen derives from provider
// schemas, and (3) off the provider process's own GetProviderSchema. The rule
// reaches every data source of every cloud provider an estate is built on -
// for the aws provider alone that is several hundred data source types, not
// the three that found it - and a future cloud provider this fork supports is
// covered the day an estate declares a managed resource of it, with no edit
// here.
func LiveProviders(cfg *configs.Config, declared map[addrs.Provider]map[string]bool) map[addrs.Provider]bool {
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
			provider := node.Module.ProviderForLocalConfig(rc.ProviderConfigAddr())
			if types, known := declared[provider]; known && !types[rc.Type] {
				// The configuration bound this local name to a provider that
				// does not serve the type. Whatever this block is, it is not
				// evidence that the run reads the live system through that
				// provider.
				continue
			}
			live[provider] = true
		}
		for _, child := range node.Children {
			walk(child)
		}
	}
	walk(cfg)
	return live
}

// Boundary answers, for one run and one demand class, whether the read phase
// may configure a given provider. It is the whole of the two-tier rule
// described above, in one object, so the classification half
// ([analyzer.confineToBoundary]) and the structural half (the command
// layer's provider seam) cannot draw the line two different ways.
//
// The zero value allows everything; use [NewBoundary].
type Boundary struct {
	// live is tier 2: providers this configuration manages a live object
	// through. See [LiveProviders].
	live map[addrs.Provider]bool

	// declared is what each provider's own schema says it serves, or nil.
	// See [Options.ProviderManagedTypes].
	declared map[addrs.Provider]map[string]bool

	// scoped mirrors [Analysis.scoped]: true selects tier 2, false tier 1.
	scoped bool
}

// NewBoundary builds the boundary for one analysis. scoped must be that
// analysis's own [Analysis.Scoped], which is what selects the tier.
func NewBoundary(cfg *configs.Config, declared map[addrs.Provider]map[string]bool, scoped bool) Boundary {
	return Boundary{live: LiveProviders(cfg, declared), declared: declared, scoped: scoped}
}

// BoundaryFor builds the boundary for an analysis that already exists, taking
// the tier from the analysis rather than from the caller. The command layer
// uses it so that a call site cannot pair a scoped analysis with an unscoped
// boundary.
func BoundaryFor(cfg *configs.Config, analysis *Analysis, declared map[addrs.Provider]map[string]bool) Boundary {
	return NewBoundary(cfg, declared, analysis.Scoped())
}

// Allows reports whether the read phase may configure this provider.
//
// crossStack exempts the two separately-ruled cross-stack read classes,
// #179's stages 2 and 3. terraform_remote_state is read through the builtin
// terraform provider, whose only managed type is logical, so neither tier
// admits it; tfe_outputs is read through hashicorp/tfe, which no choudoufu
// estate manages objects through. Both are deliberate, shipped read classes
// with their own eligibility gates (an auth surface for tfe_outputs, a
// configurable backend for terraform_remote_state) and their own refusal
// summaries, both read a remote API, and neither can run a local program -
// the boundary's actual subject. Excluding them would delete two features to
// close nothing. The exemption is per SOURCE rather than per provider, so it
// cannot widen to that provider's other data sources.
func (b Boundary) Allows(provider addrs.Provider, crossStack bool) bool {
	if crossStack || b.live[provider] {
		return true
	}
	if b.scoped {
		return false
	}
	return b.servesLiveObjects(provider)
}

// ReadableProviders flattens a [Boundary] into the provider set the READ
// phase may configure for one analysis, which is the shape the command
// layer's provider seam needs: the seam is handed a provider configuration
// address and nothing else, so it cannot ask the per-source questions
// [Boundary.Allows] asks.
//
// It deliberately does NOT consult [Source.Eligible]. The seam exists to
// catch a classification that went wrong or was bypassed - see live_plan.go's
// liveProviderReads - and a set built from the classification's own verdicts
// would catch neither. Only the provider and the source's structural class
// (its cross-stack flavor, which is a property of the type name, not a
// verdict) decide membership.
//
// Flattening loses the per-source cross-stack exemption: a provider allowed
// in because ONE of its sources is cross-stack is allowed in for the others
// too. That widening is bounded and harmless - the classification still
// refuses those other sources, so the read phase never asks - and it is the
// price of a seam that owns the provider handle.
func ReadableProviders(cfg *configs.Config, analysis *Analysis, declared map[addrs.Provider]map[string]bool) map[addrs.Provider]bool {
	b := BoundaryFor(cfg, analysis, declared)
	allowed := make(map[addrs.Provider]bool, len(b.live))
	for p := range b.live {
		allowed[p] = true
	}
	if cfg == nil || analysis == nil {
		return allowed
	}
	for _, src := range analysis.Demanded() {
		if src.Config == nil {
			continue
		}
		node := cfg.Descendent(src.Module)
		if node == nil || node.Module == nil {
			continue
		}
		provider := node.Module.ProviderForLocalConfig(src.Config.ProviderConfigAddr())
		if b.Allows(provider, src.crossStack()) {
			allowed[provider] = true
		}
	}
	return allowed
}

// servesLiveObjects is tier 1: does this provider's OWN SCHEMA declare a
// managed resource type that is not logical.
//
// A provider this run has no schema for answers true - the absence of
// evidence, not evidence of absence. That fail-open is safe rather than
// merely convenient, and the reason is worth writing down: the only way to
// have no schema for a provider is to have failed to start its plugin, and a
// phase that cannot start a provider's plugin cannot make it read anything
// either. data "external" cannot run a program through a provider process
// that does not exist. Failing CLOSED here would instead turn every plugin
// that would not start into a wall of data-read refusals blaming the
// configuration for an installation problem.
func (b Boundary) servesLiveObjects(provider addrs.Provider) bool {
	types, known := b.declared[provider]
	if !known {
		return true
	}
	for name := range types {
		if _, logical := lint.ClassifyLogicalType(name); !logical {
			return true
		}
	}
	return false
}

// AnalyzeRootOutputs derives which data sources the configuration's
// root-level `output` blocks reach and classifies each one, offline, exactly
// as [Analyze] classifies an identity-demanded source, under the same
// provider [Boundary] at its stricter tier - see [LiveProviders].
//
// The result is an [Analysis] like any other, so [ReadForOutputs] can read it
// with the same machinery, in the same dependency order. Nothing here is
// fatal and nothing here refuses a configuration: an ineligible source simply
// carries its reason and is skipped at read time.
//
// GitHub issue #352's -target scope used to be checked here, over the demand
// roots. It is checked inside [analyzer.classify] instead, because classify
// recurses and an out-of-scope source demanded only as an in-scope source's
// DEPENDENCY never passed through a check over the roots - so a -target run
// still read it.
func AnalyzeRootOutputs(ctx context.Context, cfg *configs.Config, opts Options) *Analysis {
	a := &Analysis{sources: make(map[string]*Source), projectManaged: !opts.SkipManagedProjection, scoped: true}
	if cfg == nil || cfg.Module == nil || cfg.Module.StaticEvaluator == nil || len(cfg.Module.Outputs) == 0 {
		return a
	}
	an := &analyzer{ctx: ctx, cfg: cfg, analysis: a, schemas: opts.Schemas, scope: opts.Scope, visiting: make(map[string]bool)}
	if a.projectManaged {
		an.proj = newManagedProjector(ctx, cfg, false, nil)
	}

	for _, want := range rootOutputDataDemand(cfg) {
		if _, seen := a.sources[sourceKey(want.module, want.resource)]; seen {
			continue
		}
		an.classify(want.module, want.resource, want.neededBy)
	}
	an.confineToBoundary(cfg, opts)
	return a
}

// confineToBoundary applies the phase's provider [Boundary] to every source
// this analysis classified, turning an otherwise-eligible source of a
// provider the tier in force excludes into an ineligible one. It never makes
// an ineligible source eligible.
//
// It runs over the finished order rather than per demand root because
// [analyzer.classify] recurses: a source demanded only as another source's
// dependency is stored by that recursion and never passes through either
// entry point's own loop. That recursion trap is the same one the -target
// scope check fell into until the check moved into classify itself.
//
// [analyzer.propagateIneligibility] runs afterwards because a source whose
// DEPENDENCY the boundary just excluded cannot be read either, and its own
// classification was decided before the exclusion existed.
func (an *analyzer) confineToBoundary(cfg *configs.Config, opts Options) {
	if len(an.analysis.order) == 0 {
		// The common case, and it must stay free: a configuration whose
		// identities demand no data source pays nothing for a boundary with
		// nothing to confine. [LiveProviders] walks the whole module tree.
		return
	}
	b := NewBoundary(cfg, opts.ProviderManagedTypes, an.analysis.scoped)
	for _, src := range an.analysis.order {
		if !src.Eligible {
			continue
		}
		node := an.cfg.Descendent(src.Module)
		if node == nil || node.Module == nil || src.Config == nil {
			continue
		}
		provider := node.Module.ProviderForLocalConfig(src.Config.ProviderConfigAddr())
		if b.Allows(provider, src.crossStack()) {
			continue
		}
		src.Eligible = false
		src.ReasonSummary = SummaryProviderNotLive
		src.ReasonDetail = an.notLiveDetail(src, provider)
	}
	an.propagateIneligibility()
}

// notLiveDetail is [SummaryProviderNotLive]'s sentence, and it differs by
// demand class because the two classes cost the operator different things and
// a refusal that does not say what it costs is not actionable.
func (an *analyzer) notLiveDetail(src *Source, provider addrs.Provider) string {
	if an.analysis.scoped {
		return fmt.Sprintf(
			"%s's value would settle the root output %s, but %s manages no live object in this configuration, so this run is not already reading the live system through it and a pre-plan read of it would be a new kind of side effect rather than one more read of an API the projection already reads. The output keeps the value the plan itself computes for it.",
			src.Resource.String(), src.NeededBy, provider.String())
	}
	return fmt.Sprintf(
		"%s's value is needed to resolve the identity of %s, but %s manages no live object in this configuration, so this run is not already reading the live system through it. live-plan's pre-plan phase makes read-only calls to the same remote APIs the projection already reads and does nothing else - a provider reached only through its data sources may read the machine running the plan instead (data \"external\" runs a program named by its own arguments), and running one to work out where to write a marker is not something a plan may do. Give %s an identity this phase can read - move the value into the configuration, or into a resource this estate manages live - or resolve it through a provider this configuration manages objects through.",
		src.Resource.String(), src.NeededBy, provider.String(), src.NeededBy)
}

// propagateIneligibility re-runs classification's own dependency propagation
// after [analyzer.confineToBoundary] has changed answers underneath it.
// A source classified eligible before its dependency was excluded must not
// stay eligible: the read phase reads in dependency order, and a dependent
// whose dependency was never read has nothing to evaluate its arguments
// against.
//
// The loop repeats until nothing changes, which is bounded by the number of
// classified sources: each pass either marks at least one source ineligible
// or stops.
func (an *analyzer) propagateIneligibility() {
	for {
		changed := false
		for _, src := range an.analysis.order {
			if !src.Eligible {
				continue
			}
			for _, dep := range src.Deps {
				depSrc, ok := an.analysis.sources[dep.key()]
				if !ok || depSrc.Eligible {
					continue
				}
				src.Eligible = false
				src.ReasonSummary = SummaryNotReadable
				src.ReasonDetail = fmt.Sprintf(
					"%s's value is needed by %s, but it depends on %s, which cannot be read before the plan (%s), so it cannot be read before the plan.",
					src.Resource.String(), src.NeededBy, dep.Resource.String(), depSrc.ReasonDetail)
				changed = true
				break
			}
		}
		if !changed {
			return
		}
	}
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
