// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package identity

import (
	"context"
	"sort"

	"github.com/hashicorp/hcl/v2"

	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/configs"
	"github.com/intentius/choudoufu/internal/tfdiags"
)

// This file is the config-side half of the answer to the one question a
// provider's schemas cannot settle: for the identity attributes that a
// legacy-SDK schema marks Optional+Computed, is the name in the
// configuration or in the cloud?
//
// aws_s3_bucket's bucket argument and aws_vpc's id attribute are the same
// shape in a schema and opposite answers (see the package documentation).
// Nothing in the protocol separates them. But a configuration does, per
// instance and without ambiguity: a block that writes bucket = "…" is
// naming the object it owns, and a block that writes no such argument is
// letting the cloud name it. That is not an inference about the type, it is
// a reading of the configuration, and it is exactly what resolution is
// already walking the configuration to do.
//
// So the signal collected here is deliberately narrow and dumb: which
// top-level arguments does each resource instance set to a non-null value.
// It names no identity attributes and consults no schema, because the
// schemas arrive one phase later, behind a running provider. Combining the
// two is [DerivableWith]'s job.

// Naming is what a configuration says about who names a resource: the
// configuration itself, or the cloud.
//
// It is a claim, not a proof. A configuration that sets an identity
// argument is asserting that it names the object; a configuration that
// leaves it unset is asserting that it does not. Both assertions are the
// operator's to make, and both are the ones an import has to act on.
type Naming string

const (
	// NamingClientNamed: every identity argument asked about is set, on
	// every instance asked about. The configuration names these objects.
	NamingClientNamed Naming = "client-named"

	// NamingServerAssigned: none of the identity arguments is set on any
	// instance. Nothing in the configuration names these objects, so their
	// names come from the cloud and only discovery finds them.
	NamingServerAssigned Naming = "server-assigned"

	// NamingPartial: some are set and some are not, whether across the
	// arguments of one instance or across the instances of one type. It is
	// its own answer rather than a rounding of the other two: a type whose
	// identity needs two arguments and whose config supplies one cannot be
	// admitted, and a type half of whose instances name themselves is a
	// per-instance question no type-level verdict may flatten.
	NamingPartial Naming = "partial"

	// NamingUnknown: nothing to go on. No instance of the type is in the
	// configuration, or no argument was asked about.
	NamingUnknown Naming = "unknown"
)

// InstanceNaming is one instance's answer, with both halves named so a
// report can say which argument decided it.
type InstanceNaming struct {
	// Addr is the resource instance, including its count or for_each key.
	Addr addrs.AbsResourceInstance

	// Naming is this instance's verdict over the arguments asked about.
	Naming Naming

	// Set and Unset partition those arguments, each sorted.
	Set, Unset []string
}

// ConfigSignal is which arguments each managed resource instance in one
// configuration sets: the config-side signal that decides the cohort the
// provider's schemas leave ambiguous.
//
// It is collected by [Resolve] as it walks, and by [ScanConfig] on its own.
// Like everything else in this package it reads only the configuration:
// no provider, no process, no cloud.
//
// "Set" means the argument is written in the resource block and does not
// evaluate to null. The nullness check is what makes the common
// name = var.name idiom honest: a variable defaulting to null leaves the
// name to the cloud however the block is written, and a signal that counted
// the argument present would claim a name the apply will not use. An
// argument whose value this package cannot evaluate - it references another
// resource, or a data source, or an impure function - counts as set,
// because the configuration is supplying a value either way and only the
// value is out of reach.
type ConfigSignal struct {
	// set maps an instance address to the arguments it sets.
	set map[string]map[string]bool

	// byType lists each type's instance addresses in configuration order.
	byType map[string][]addrs.AbsResourceInstance

	// count is the number of instances recorded.
	count int
}

func newConfigSignal() *ConfigSignal {
	return &ConfigSignal{
		set:    make(map[string]map[string]bool),
		byType: make(map[string][]addrs.AbsResourceInstance),
	}
}

func (s *ConfigSignal) add(addr addrs.AbsResourceInstance, set map[string]bool) {
	key := addr.String()
	if _, exists := s.set[key]; exists {
		return
	}
	s.set[key] = set
	typeName := addr.Resource.Resource.Type
	s.byType[typeName] = append(s.byType[typeName], addr)
	s.count++
}

// Len is the number of resource instances the signal covers.
func (s *ConfigSignal) Len() int {
	if s == nil {
		return 0
	}
	return s.count
}

// Types lists every managed resource type the configuration declares,
// sorted. Unlike the rest of this package it is not restricted to the
// admission table: a type the table has never heard of is exactly the kind
// this signal exists to say something about.
func (s *ConfigSignal) Types() []string {
	if s == nil {
		return nil
	}
	out := make([]string, 0, len(s.byType))
	for typeName := range s.byType {
		out = append(out, typeName)
	}
	sort.Strings(out)
	return out
}

// Instances lists one type's instance addresses, in configuration order.
func (s *ConfigSignal) Instances(typeName string) []addrs.AbsResourceInstance {
	if s == nil {
		return nil
	}
	return s.byType[typeName]
}

// Sets reports whether one instance sets one argument to a non-null value.
func (s *ConfigSignal) Sets(addr addrs.AbsResourceInstance, arg string) bool {
	if s == nil {
		return false
	}
	return s.set[addr.String()][arg]
}

// NamingOf answers for one instance and one set of identity arguments,
// which the caller takes from a provider's identity schema.
func (s *ConfigSignal) NamingOf(addr addrs.AbsResourceInstance, args []string) InstanceNaming {
	out := InstanceNaming{Addr: addr, Naming: NamingUnknown}
	if s == nil || len(args) == 0 {
		return out
	}
	if _, known := s.set[addr.String()]; !known {
		return out
	}
	for _, arg := range args {
		if s.Sets(addr, arg) {
			out.Set = append(out.Set, arg)
			continue
		}
		out.Unset = append(out.Unset, arg)
	}
	sort.Strings(out.Set)
	sort.Strings(out.Unset)

	switch {
	case len(out.Unset) == 0:
		out.Naming = NamingClientNamed
	case len(out.Set) == 0:
		out.Naming = NamingServerAssigned
	default:
		out.Naming = NamingPartial
	}
	return out
}

// NamingOfType is the type-level verdict over every instance of a type,
// with the per-instance answers alongside.
//
// The type verdict is the unanimous one or [NamingPartial]. Unanimity is
// the bar because admission is a property of the type: a table row saying
// "this type's name is in configuration" that half the configuration's
// instances contradict is a row that would import the wrong object for the
// other half. Disagreement is reported as disagreement, and the
// per-instance list says where.
func (s *ConfigSignal) NamingOfType(typeName string, args []string) (Naming, []InstanceNaming) {
	if s == nil || len(args) == 0 {
		return NamingUnknown, nil
	}
	insts := s.byType[typeName]
	if len(insts) == 0 {
		return NamingUnknown, nil
	}

	out := make([]InstanceNaming, 0, len(insts))
	verdict := NamingUnknown
	for _, addr := range insts {
		one := s.NamingOf(addr, args)
		out = append(out, one)
		switch {
		case verdict == NamingUnknown:
			verdict = one.Naming
		case verdict != one.Naming:
			verdict = NamingPartial
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Addr.String() < out[j].Addr.String() })
	return verdict, out
}

// ScanConfig collects the config-side naming signal for a whole
// configuration, without classifying anything.
//
// It is [Resolve] minus the identity table: every managed resource
// instance is expanded and read, including instances of types the table
// does not cover, because "which types could join the table" is a question
// about exactly those. Nothing here fails on an unknown type.
//
// The diagnostics are the expansion's own - a count or a for_each that
// cannot be enumerated from configuration - and they are advisory. A caller
// building a report can log them; the signal covers whatever did expand.
func ScanConfig(ctx context.Context, cfg *configs.Config) (*ConfigSignal, tfdiags.Diagnostics) {
	var diags tfdiags.Diagnostics

	if cfg == nil || cfg.Module == nil || cfg.Module.StaticEvaluator == nil {
		diags = diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "No configuration to scan",
			Detail:   "The config-side naming signal is read from a configuration loaded with a static evaluator, and none was given.",
		})
		return newConfigSignal(), diags
	}

	r := newResolver(ctx, cfg, Context{})
	signal := r.collectSignal(cfg)
	return signal, r.diags
}

// collectSignal walks every managed resource in the module and records what
// each of its instances sets. It is the shared body of [ScanConfig] and of
// the collection [ResolveIn] does as it goes.
func (r *resolver) collectSignal(cfg *configs.Config) *ConfigSignal {
	signal := newConfigSignal()
	for _, rc := range sortedResources(cfg.Module.ManagedResources) {
		exp, ok := r.expansionFor(rc)
		if !ok {
			continue
		}
		r.signalFor(signal, rc, exp)
	}
	return signal
}

// signalFor records one resource block's instances.
func (r *resolver) signalFor(signal *ConfigSignal, rc *configs.Resource, exp *expansion) {
	// JustAttributes on a resource body reports a diagnostic per nested
	// block and returns the top-level arguments anyway, which is precisely
	// what is wanted: a nested block is not an identity argument, and the
	// diagnostic is about a body this call has no business decoding. The
	// meta-arguments (count, for_each, provider, depends_on) are already
	// hidden by the time configs hands the body over.
	attrs, _ := rc.Config.JustAttributes()

	names := make([]string, 0, len(attrs))
	for name := range attrs {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, key := range exp.keys {
		addr := rc.Addr().Instance(key).Absolute(addrs.RootModuleInstance)
		scope := exp.scope(key)
		set := make(map[string]bool, len(names))
		for _, name := range names {
			attr := attrs[name]
			if r.argIsSet(addr, attr, scope) {
				set[name] = true
			}
		}
		signal.add(addr, set)
	}
}

// argIsSet reports whether one argument supplies a value for one instance.
//
// Everything this cannot see through counts as set, and that asymmetry is
// deliberate. The signal's job is to separate "the configuration writes a
// name here" from "the configuration writes nothing here"; an argument
// whose value is a reference, or an impure call, or an expression that will
// not evaluate, is still written. Only an argument that is absent, or that
// evaluates to null, is a configuration declining to name the object.
func (r *resolver) argIsSet(addr addrs.AbsResourceInstance, attr *hcl.Attribute, scope instScope) bool {
	if attr == nil {
		return false
	}
	if r.isSymbolic(attr.Expr, scope) {
		return true
	}
	if len(impureCallsIn(attr.Expr)) > 0 {
		return true
	}
	val, diags := r.evalPure(attr.Expr, scope, r.identifier(addr, attr.Name, attr.Range))
	if diags.HasErrors() {
		return true
	}
	return !val.IsNull()
}
