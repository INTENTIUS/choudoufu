// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package configs

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/ext/dynblock"
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/intentius/choudoufu/internal/tfdiags"
	"github.com/zclconf/go-cty/cty"
	"github.com/zclconf/go-cty/cty/function"
)

// StaticIdentifier holds a Referenceable item and where it was declared
type StaticIdentifier struct {
	Module    addrs.Module
	Subject   string
	DeclRange hcl.Range
}

func (ref StaticIdentifier) String() string {
	val := ref.Subject
	if len(ref.Module) != 0 {
		val = ref.Module.String() + ":" + val
	}
	return val
}

type StaticModuleVariables func(v *Variable) (cty.Value, hcl.Diagnostics)

// StaticModuleCall contains the information required to call a given module
type StaticModuleCall struct {
	addr      addrs.Module
	declRange hcl.Range
	vars      StaticModuleVariables
	rootPath  string
	workspace string
}

func NewStaticModuleCall(addr addrs.Module, declRange hcl.Range, vars StaticModuleVariables, rootPath string, workspace string) StaticModuleCall {
	return StaticModuleCall{
		addr:      addr,
		declRange: declRange,
		vars:      vars,
		rootPath:  rootPath,
		workspace: workspace,
	}
}

func (s StaticModuleCall) Variables() StaticModuleVariables {
	return s.vars
}

func (s StaticModuleCall) WithVariables(vars StaticModuleVariables) StaticModuleCall {
	return StaticModuleCall{
		addr:      s.addr,
		declRange: s.declRange,
		vars:      vars,
		rootPath:  s.rootPath,
		workspace: s.workspace,
	}
}

// only used in testing
func RootModuleCallForTesting() StaticModuleCall {
	return NewStaticModuleCall(addrs.RootModule, hcl.Range{}, func(_ *Variable) (cty.Value, hcl.Diagnostics) {
		panic("Variables have not been configured for this test!")
	}, "<testing>", "")
}

// A static evaluator contains the information required to build a EvalContext
// which only understands "static" (non-state) data. Internally, it relies
// on staticData
type StaticEvaluator struct {
	call StaticModuleCall
	cfg  *Module

	// pureOnly makes every scope this evaluator builds refuse to produce a
	// value from an impure function. See [StaticEvaluator.Pure].
	pureOnly bool

	// dataLookup, when non-nil, answers data-resource references from values
	// a caller read before evaluation began. See
	// [StaticEvaluator.WithDataResults].
	dataLookup StaticDataLookup

	// repetition holds the each.key/each.value/count.index values the
	// single resource instance whose arguments are being evaluated actually
	// has. See [StaticEvaluator.WithRepetitionData].
	repetition instances.RepetitionData

	// moduleInstance is the module INSTANCE whose expressions are being
	// evaluated, and moduleInstanceSet says whether a caller supplied one.
	// The two fields are separate because addrs.ModuleInstance's zero value
	// IS the root module instance, so the field alone cannot tell "the root"
	// from "nobody said". See [StaticEvaluator.WithModuleInstance].
	moduleInstance    addrs.ModuleInstance
	moduleInstanceSet bool

	// funcOverrides, when non-nil, replaces named entries of the scope's
	// base function table. See [StaticEvaluator.WithFunctionOverrides].
	funcOverrides map[string]function.Function

	// unknownForRefused makes every scope this evaluator builds answer a
	// resource or module-output reference with an unknown value instead of
	// refusing it, and moduleOutputs is the closure that answers a module
	// call by value where it can. Both are set together by
	// [StaticEvaluator.WithUnknownForRefusedReferences].
	unknownForRefused bool
	moduleOutputs     StaticModuleOutputLookup
}

// StaticModuleOutputLookup answers a module-call reference with the object
// of that call's own output values, or reports that it has none. The address
// is the call as written in the module doing the referring
// (`module.network` is addrs.ModuleCall{Name: "network"}); the value is the
// whole call's, an object with one attribute per output, matching how the
// plan-time evaluator shapes a module reference so that
// `module.network.configuration` resolves by ordinary attribute access.
//
// It is consulted only by an evaluator built through
// [StaticEvaluator.WithUnknownForRefusedReferences], and a false there is
// not a refusal: the reference becomes unknown like any other the tolerant
// scope cannot answer.
type StaticModuleOutputLookup func(call addrs.ModuleCall) (cty.Value, bool)

// WithUnknownForRefusedReferences returns a copy of the evaluator whose
// scopes substitute an UNKNOWN value for a managed-resource, data-source or
// module-output reference instead of refusing it, and which answer a module
// call by value through outputs where outputs can.
//
// # What this is for
//
// Stock OpenTofu's plan-time evaluator has no strict/loose distinction to
// make. A reference it cannot answer yet becomes an unknown and evaluation
// continues, so an object with one apply-time leaf is a KNOWN object with an
// unknown attribute, and everything the configuration derives from the parts
// it did write down - a map's key set, a literal sibling, a conditional on a
// literal flag - still has an answer. This fork's static evaluator refuses
// the whole enclosing value instead, and that difference is choudoufu
// refusing where stock proceeds rather than a fact about the configuration.
//
// [identity.resolver] already brought the two together for one shape, a
// module-call ARGUMENT whose skeleton is literal and one of whose leaves is
// not (internal/live/identity/partialargs.go). That rebuild works one
// constructor element at a time and therefore reaches only what the caller
// wrote out AT the call. The same poisoning happens one layer in - inside a
// local value, and inside a module output's own expression - where there is
// no constructor for a caller to rebuild, and this is the seam for it.
//
// # Why an unknown, and why that cannot widen what becomes a marker
//
// The substitution is an unknown, never a guess, so any value that comes
// back KNOWN is independent of every reference that was substituted: it is
// the configuration's own literals, run through the configuration's own
// functions, exactly as stock would run them. A value that DID depend on a
// substituted leaf comes back unknown, and every path in this fork that
// turns a value into an identity demands a known one
// ([identity.staticSubValue] requires IsWhollyKnown,
// [identity.collectionKeyNames] rejects an unknown key, the resolver refuses
// an unknown identity argument), so a substituted leaf cannot become a
// marker. What can newly succeed is a count, a for_each key set, or an
// identity component that reads only what the configuration states outright.
//
// # What it deliberately does not tolerate
//
// count.index, each.key, each.value and provider functions keep refusing
// exactly as they did. Repetition is answered by
// [StaticEvaluator.WithRepetitionData] from a caller that has established
// WHICH instance it is asking about; substituting an unknown for it instead
// would make an expansion that reads count.index appear answerable when
// nothing has established the instance at all. A provider function's answer
// comes from a provider that has not been started.
//
// This is opt-in and additive: an evaluator nobody calls this on refuses
// every one of these references exactly as it always has.
func (s *StaticEvaluator) WithUnknownForRefusedReferences(outputs StaticModuleOutputLookup) *StaticEvaluator {
	if s == nil {
		return s
	}
	dup := *s
	dup.unknownForRefused = true
	dup.moduleOutputs = outputs
	return &dup
}

// moduleOutputsFor answers a module-call reference from the evaluator's
// module-output lookup, when it has one that covers the call. It is the
// single place both [staticScopeData.StaticValidateReferences] and
// [staticScopeData.GetModule] consult, so the two can never disagree.
func (s *StaticEvaluator) moduleOutputsFor(call addrs.ModuleCall) (cty.Value, bool) {
	if s == nil || s.moduleOutputs == nil {
		return cty.NilVal, false
	}
	return s.moduleOutputs(call)
}

// StaticDataLookup answers a data-resource reference with a value read ahead
// of static evaluation, or reports that it has none. The address is the
// module-relative resource ([addrs.Resource] with
// [addrs.DataResourceMode]); the value is the whole resource's: the single
// instance's object for an unexpanded block, a tuple for count, an object
// keyed by string for for_each, matching how the plan-time evaluator shapes
// a resource reference so that instance indexing works unchanged.
type StaticDataLookup func(addr addrs.Resource) (cty.Value, bool)

// WithDataResults returns a copy of the evaluator whose scopes answer
// data-resource references through the given lookup instead of refusing them
// as dynamic values.
//
// The base evaluator refuses every resource reference, because static
// evaluation runs before anything has been read and inventing a value here
// would be a guess a later phase treats as fact. A caller that HAS performed
// reads - the pre-resolution data-read phase - hands the results in through
// this seam, and only references the lookup actually covers are permitted;
// everything else keeps refusing exactly as before.
//
// A managed-mode reference is answerable only from the resource block's own
// configuration - never from a pre-plan read, because there is nothing such
// a read could honestly say about an object the plan may be about to change.
// internal/live/dataread's lookup answers one when the block's own body sets
// an argument of that name and the expression evaluates statically; the
// answer is partial by construction, so [lookupCoversTraversal] refuses a
// reference that does not take it down to one of those arguments.
func (s *StaticEvaluator) WithDataResults(lookup StaticDataLookup) *StaticEvaluator {
	if s == nil || lookup == nil {
		return s
	}
	dup := *s
	dup.dataLookup = lookup
	return &dup
}

// WithFunctionOverrides returns a copy of the evaluator whose scopes use
// overrides in place of the named entries of the base function table
// ([lang.Scope.FuncOverrides]), for every reference this evaluator resolves
// at any depth - the same "carries through nested scopes" property
// [StaticEvaluator.WithRepetitionData] documents, because [newStaticScope]
// builds every nested scope from this same *StaticEvaluator.
//
// internal/live/dataread is the first and, as of this writing, only caller:
// issue #193's length()/keys() guard needs to see whether the OBJECT a data
// source's argument passes to either function is an unexpanded managed
// resource's own projection, a fact [configs.lookupCoversTraversal] and
// plain attribute access cannot be made to draw without over-refusing a
// legitimate for_each-expanded reference (see managedproj.go's doc). A
// function table override is the one seam that can see the argument VALUE
// at the exact place length()/keys() are invoked without changing what an
// ordinary `.subnet_id`-style reference resolves to, because it replaces
// only those two function-table entries and never touches attribute access
// at all.
func (s *StaticEvaluator) WithFunctionOverrides(overrides map[string]function.Function) *StaticEvaluator {
	if s == nil || len(overrides) == 0 {
		return s
	}
	dup := *s
	dup.funcOverrides = overrides
	return &dup
}

// WithRepetitionData returns a copy of the evaluator whose scopes answer
// each.key, each.value and count.index from rd, for every reference this
// evaluator resolves at any depth - not only the expression a caller hands
// straight to [StaticEvaluator.Evaluate], but also any local value or
// module-call variable that expression reaches through [StaticEvaluator]'s
// own reference resolution (a local's own defining expression evaluates
// through a freshly built child scope, and that scope carries the same
// [*StaticEvaluator], so it sees the same rd).
//
// rd is a value bag, not a source of truth: whatever the caller passes is
// what a reference gets back, with no independent check that it matches the
// resource instance actually being evaluated. The caller - identity
// resolution's own per-instance expansion - is the party responsible for
// that, because only it knows which instance's arguments it is asking this
// evaluator to answer. A field of rd left at cty.NilVal (the zero value)
// means "not known for this instance", which behaves exactly as an
// evaluator built with no repetition data at all: an unset each.value under
// a for_each whose values are not statically known must still refuse a
// reference to it, not answer with something invented.
//
// The base evaluator refuses every each/count reference (see
// [staticScopeData.GetCountAttr] and [staticScopeData.GetForEachAttr]),
// because plain static evaluation - module source, backend configuration,
// module call variables - has no notion of a resource instance at all. A
// caller resolving one instance's identity is the first caller of this
// package that does.
func (s *StaticEvaluator) WithRepetitionData(rd instances.RepetitionData) *StaticEvaluator {
	if s == nil {
		return s
	}
	dup := *s
	dup.repetition = rd
	return &dup
}

// WithModuleInstance returns a copy of the evaluator that knows WHICH
// instance of its module it is evaluating for, and therefore can answer this
// fork's one added evaluator symbol, [markers.ModulePrefixAttr] - see
// [staticScopeData.GetTerraformAttr].
//
// It carries through nested scopes exactly as
// [StaticEvaluator.WithRepetitionData] does, and for the same mechanical
// reason: [newStaticScope] builds every nested scope from this same
// *StaticEvaluator, so a local value or module-call variable reached from
// the expression a caller hands to Evaluate sees the same instance.
//
// The base evaluator does NOT know its module instance, and must not guess
// one. A StaticEvaluator belongs to a *configs.Module, and a module with a
// for_each'd or count'd call above it has one static evaluator shared by
// every instance of it - the exact fact issue #378 is about. Answering the
// symbol from an evaluator nobody threaded an instance into would answer it
// for whichever instance happened to be nearest, or for none, and a marker
// built on that answer is a WRONG marker on a real object rather than a
// missing one. So the refusal in GetTerraformAttr is the load-bearing half
// of this seam, not an edge case of it: this method exists to turn that
// refusal off for exactly the caller that has established the instance, and
// for nobody else.
//
// A caller that genuinely evaluates for the root module passes
// addrs.RootModuleInstance, which is answered as a refusal of its own (the
// root has no module prefix; see [markers.ModulePrefix]) rather than as "not
// threaded" - the distinction the moduleInstanceSet flag exists for.
func (s *StaticEvaluator) WithModuleInstance(modInst addrs.ModuleInstance) *StaticEvaluator {
	if s == nil {
		return s
	}
	dup := *s
	dup.moduleInstance = modInst
	dup.moduleInstanceSet = true
	return &dup
}

// WithVariables returns a copy of the evaluator whose module call answers
// var.* references (see [staticScopeData.GetInputVariable]) through vars
// instead of the one captured when the module tree was first built.
//
// The base evaluator's own vars closure ([ModuleCall.Variables], built by
// [ModuleCall.decodeStaticVariables]) is frozen once, at load time, against
// this module's OWN [*StaticEvaluator] as it existed then - before any
// caller has ever had a chance to call [StaticEvaluator.WithDataResults] or
// [StaticEvaluator.WithRepetitionData] on anything, because no read or
// instance resolution has happened yet. That closure is a real *StaticEvaluator
// pointer captured by a Go closure, not re-derived per call, so every
// module-call variable it ever answers is evaluated with NO data-source
// coverage and NO repetition data, forever, for the lifetime of that
// [*Config] tree - regardless of what coverage a later caller attaches to
// ITS OWN evaluator for a descendant module.
//
// That staleness is invisible for an ordinary var.X - source, count,
// for_each, module version - because those rarely depend on a data source
// or a resource instance's repetition. It stops being invisible the moment
// a descendant module's data source references var.X, and var.X's value,
// in the ANCESTOR, itself reads that ancestor's OWN data source (issue
// #212): the ancestor's frozen evaluator has never seen a data-source
// lookup at all, so the reference refuses as unreadable even when #179
// stage 3 says this exact shape should be readable.
//
// A caller that has built a "live" evaluator for the ancestor module -
// carrying that module's own [StaticEvaluator.WithDataResults] coverage,
// scoped to that module and no other (see
// internal/live/dataread's per-module lookup, never a lookup shared across
// two different modules) - uses WithVariables to re-point THIS module's own
// var.* resolution at that live ancestor evaluator instead of the frozen
// one, via [ModuleCall.VariablesUsing]. Every other field this evaluator
// carries (its own module, its own data lookup, its own repetition) is
// untouched: only how ITS OWN var.* references reach their ancestor's
// expression changes.
func (s *StaticEvaluator) WithVariables(vars StaticModuleVariables) *StaticEvaluator {
	if s == nil || vars == nil {
		return s
	}
	dup := *s
	dup.call = dup.call.WithVariables(vars)
	return &dup
}

// repetitionAttr answers a count.<name> or each.<name> reference from the
// evaluator's repetition data, reporting whether it is covered. It is the
// single place both [staticScopeData.StaticValidateReferences] (whether to
// refuse) and [staticScopeData.GetCountAttr]/[staticScopeData.GetForEachAttr]
// (what to return) consult, so the two can never disagree about which
// references are answerable.
func (s *StaticEvaluator) repetitionAttr(subject addrs.Referenceable) (cty.Value, bool) {
	if s == nil {
		return cty.NilVal, false
	}
	switch a := subject.(type) {
	case addrs.CountAttr:
		if a.Name == "index" && s.repetition.CountIndex != cty.NilVal {
			return s.repetition.CountIndex, true
		}
	case addrs.ForEachAttr:
		switch a.Name {
		case "key":
			if s.repetition.EachKey != cty.NilVal {
				return s.repetition.EachKey, true
			}
		case "value":
			if s.repetition.EachValue != cty.NilVal {
				return s.repetition.EachValue, true
			}
		}
	}
	return cty.NilVal, false
}

// Creates a static evaluator based from the given module and module call
func NewStaticEvaluator(mod *Module, call StaticModuleCall) *StaticEvaluator {
	return &StaticEvaluator{
		call: call,
		cfg:  mod,
	}
}

// baseDir is the directory file(), fileexists(), fileset(), the filehash
// functions, templatefile() and templatestring() resolve a bare relative
// path against (see [lang.Scope.BaseDir] and makeBaseFunctionTable in
// internal/lang/functions.go, which both this evaluator's scopes and
// internal/tofu's dynamic-phase evaluator (internal/tofu/evaluate.go's
// Evaluator.Scope) feed into the same function table).
//
// Upstream hardcodes this to the literal string "." for every module at
// every phase - see internal/tofu/evaluate.go's Evaluator.Scope, which
// carries the identical "Always current working directory for now" comment
// this replaces. "." resolves through the standard library against the
// process's actual working directory, which upstream never varies per
// module: a child module's bare file("x") is resolved against the same
// directory as the root's, not the child's own directory. Verified against
// real `terraform plan` 1.15.8: a child module's file("data.txt") fails
// unless the file sits next to the ROOT module's config, and file("child/
// data.txt") succeeds from the root — i.e. relative paths in file() and
// friends resolve against wherever the process's cwd was when the user
// invoked terraform/tofu, which by convention is the root module's own
// directory (the directory named by path.root), not path.module.
//
// This fork evaluates every module in a configuration tree - root and every
// descendant - from one process invocation whose actual os.Getwd() is
// wherever the calling tool started (a corpus sweep's repo root, a test
// binary's package directory), never the config's own directory. "." is
// therefore always wrong here in exactly the way it happens to be right
// under upstream's single-directory-per-invocation assumption. The fix is
// to make explicit what upstream leaves implicit: resolve against the root
// module's own directory, call.rootPath - the same value GetPathAttr
// already returns for path.root (see staticScopeData.GetPathAttr's "root"
// case below), populated for the root call by every real construction site
// (internal/command/meta_config.go's rootModuleCall, internal/live/check/
// load.go's Load) and propagated to every descendant by
// internal/configs/config_build.go's buildChildModules, which always passes
// parent.Root.Module.SourceDir - never the child's own directory - as the
// child's rootPath.
//
// call.rootPath is empty only for a StaticModuleCall built directly as a
// zero value rather than through NewStaticModuleCall (some configload
// tests do this to exercise LoadConfigDir alone, with no static evaluation
// of file-reading functions in play). cfg.SourceDir - the module's own
// directory - is the next best fallback for that case, since it is at
// least the directory that was actually loaded; "." is the last resort,
// preserving the old behaviour exactly when neither is available.
func (s *StaticEvaluator) baseDir() string {
	if s == nil {
		return "."
	}
	if s.call.rootPath != "" {
		return s.call.rootPath
	}
	if s.cfg != nil && s.cfg.SourceDir != "" {
		return s.cfg.SourceDir
	}
	return "."
}

// modulePath is path.module's value: the calling module's own directory,
// expressed relative to call.rootPath - the same anchor [baseDir] resolves
// bare relative paths against - rather than cfg.SourceDir's literal value.
//
// cfg.SourceDir is a real, disk-resolvable path in this fork for every
// module, root or descendant: internal/configs/config_build.go's
// buildChildModules and internal/live/check/load.go's resolveModuleDir both
// compute a descendant's SourceDir as filepath.Join(parent's own SourceDir,
// the local module-call source), chained down from the root's SourceDir -
// which, unlike stock, is never the literal ".". Stock computes a child's
// path.module with the identical Join-chain, but because its root SourceDir
// IS always "." (a real terraform/tofu invocation requires the process's
// actual cwd to already be the root module's directory), the chain stays a
// short, portable, root-relative string like "." or "modules/foo" - it
// never accumulates the root's own real location as a literal prefix the
// way this fork's does.
//
// That difference is invisible for a bare file("x") - baseDir already
// supplies the real root location once - but it corrupts every
// "${path.module}/x"-style call, which is how most real configurations use
// file()/templatefile() specifically because a bare relative path is
// unreliable (see baseDir's own doc). With cfg.SourceDir's literal value at
// the root, file("${path.module}/x")'s argument is "rootPath/x"; joining
// that against baseDir=rootPath produces "rootPath/rootPath/x" - a path
// that happens not to exist is a loud refusal, exactly like today's bug,
// but a directory that happens to nest itself (a module named the same as
// its own parent, which real repositories do have) makes it silently read
// a different real file. Verified by planting a file at the doubled
// location and watching a "${path.module}/x"-style file() read it instead
// of the correct one, before this fix; expressing path.module relative to
// rootPath - "." at the root, "modules/foo" for a child two levels under a
// root two levels of its own away from cfg.SourceDir - collapses the join
// back to the single, correct path, matching stock's own chain exactly
// once rootPath stands in for stock's implicit ".".
//
// path.root deliberately does NOT get this same relative treatment (see
// GetPathAttr's "root" case): configurations across this fork's own corpus
// use basename(abspath(path.root)) to recover the deployment directory's
// own name for a tag value, which needs the real directory, not ".". It
// gets [StaticEvaluator.rootDir]'s different fix instead - made absolute
// rather than relative - which closes the doubling issue #226 found in
// this same "no corpus configuration combines path.root with a
// file()-family call" gap this comment used to describe as theoretical;
// see rootDir's own doc for why an absolute value is what stops the
// doubling for path.root specifically, where modulePath's relative
// approach does not apply.
//
// Falls back to cfg.SourceDir unchanged - this method's behaviour before
// this fix - when there is no rootPath to make the value relative to, or
// when SourceDir cannot be expressed relative to it (mismatched
// absolute/relative-ness between the two, which every real construction
// site avoids by chaining both from the same base, but a hand-built
// StaticModuleCall in a test might not).
func (s *StaticEvaluator) modulePath() string {
	if s == nil || s.cfg == nil {
		return ""
	}
	if s.call.rootPath == "" {
		return s.cfg.SourceDir
	}
	rel, err := filepath.Rel(s.call.rootPath, s.cfg.SourceDir)
	if err != nil {
		return s.cfg.SourceDir
	}
	return rel
}

// rootDir is path.root's value: the root module's own directory, made
// absolute.
//
// path.root deliberately stays anchored to the real root directory rather
// than being made relative to it the way [StaticEvaluator.modulePath] makes
// path.module relative - see modulePath's own doc for why: this fork's
// corpus uses basename(abspath(path.root)), 25+ sites across
// govuk-infrastructure and govuk-aws, to recover the deployment directory's
// own name, and that needs the real directory, not ".".
//
// But call.rootPath's raw string - what this method returned before this
// fix (issue #226) - is the exact same string [StaticEvaluator.baseDir]
// resolves every bare file()-family argument against. "${path.root}/x"
// then doubles exactly the way modulePath's doc describes for
// "${path.module}/x" before ITS fix: the argument becomes "rootPath/x",
// and openFile (internal/lang/funcs/filesystem.go) joins that onto
// baseDir=rootPath, producing "rootPath/rootPath/x". modulePath's fix does
// not transfer here - there is no relative form of path.root that stays
// "." at the root and still survives basename(abspath(...)) the way
// path.module's does, because abspath(".") answers with the process's
// actual working directory, not the root module's, whenever this fork's
// invocation cwd is not the root (exactly the mismatch #205 exists to
// route around in the first place).
//
// Making the value absolute breaks the doubling a different way, without
// giving up path.root's own meaning: once path.root is absolute,
// "${path.root}/x" is itself an absolute path, and openFile only joins
// baseDir onto an argument that is NOT already absolute -
// filepath.IsAbs(path) is true, so the join is skipped entirely and the
// argument opens as-is, single, never doubled. basename(abspath(x)) is
// idempotent on an already-absolute, already-clean x, so every corpus
// site's answer is unchanged; see TestStaticEvaluator_rootDir for the
// direct check and TestStaticEvaluator_PathRootPrefixedFileDoesNotDouble
// for the end-to-end proof mirroring modulePath's own doubling test.
//
// The anchor is baseDir() itself, not call.rootPath directly, so the same
// rootPath-then-cfg.SourceDir-then-"." fallback chain baseDir's own doc
// explains applies here too, rather than a second copy of it. Abs can only
// fail if os.Getwd fails, a system-level condition no real construction
// site hits; falling back to the un-absolute baseDir() in that case keeps
// this method total rather than swallowing the error into ".".
func (s *StaticEvaluator) rootDir() string {
	dir := s.baseDir()
	abs, err := filepath.Abs(dir)
	if err != nil {
		return dir
	}
	return filepath.ToSlash(abs)
}

func (s *StaticEvaluator) scope(ident StaticIdentifier) *lang.Scope {
	return newStaticScope(s, ident)
}

func (s StaticEvaluator) Evaluate(ctx context.Context, expr hcl.Expression, ident StaticIdentifier) (cty.Value, hcl.Diagnostics) {
	val, diags := s.scope(ident).EvalExpr(ctx, expr, cty.DynamicPseudoType)
	return val, diags.ToHCL()
}

// EvaluateStructural evaluates expr the same way [StaticEvaluator.Evaluate]
// does, except a reference this evaluator refuses (a managed resource, a
// data source, a module output - see [staticScopeData.StaticValidateReferences])
// does not automatically veto expr's own rendered VALUE the way Evaluate's
// EvalExpr does. Ordinary evaluation treats "some reference this expression
// transitively depends on was refused" and "the value this expression
// renders is unusable" as the same fact; they are not. A tuple literal's
// LENGTH is a property of its type, independent of any one element's
// contents, so length(var.x) for a var.x whose declared value is
// [{a=1,b=SOMETHING_DYNAMIC}] still has a real answer even though b does
// not - GetInputVariable (static_scope.go) already keeps that shape's TYPE
// on a refused reference rather than collapsing it to cty.DynamicVal's
// type-erased unknown, so this method's only job is to stop discarding the
// value the instant ANY refusal fired anywhere in the reference graph, and
// ask instead whether THIS expression's own rendered result came back
// usable regardless.
//
// The gate is deliberately narrower than "ignore every error": every
// diagnostic has to carry a [RefusedReference] extra (attached only by
// StaticValidateReferences, meaning exactly "a reference named here is
// outside static scope" - never a parse error, a type mismatch, or a wrong
// argument count, none of which carry that extra), AND the rendered value
// has to come back non-null, wholly known and unmarked. Either condition
// failing returns the value and diagnostics exactly as computed, unfiltered
// - the same conservative default every refusal in this codebase falls
// back to.
//
// Nothing else calls this yet: it is additive, so every existing caller of
// Evaluate/EvalExpr keeps its current behavior unchanged.
func (s StaticEvaluator) EvaluateStructural(ctx context.Context, expr hcl.Expression, ident StaticIdentifier) (cty.Value, hcl.Diagnostics) {
	scope := s.scope(ident)
	refs, refDiags := lang.ReferencesInExpr(addrs.ParseRef, expr)
	diags := refDiags

	// EvalContextTolerant, not EvalContext: the whole point of this method
	// is that ONE refused reference inside expr must not also blank out
	// every other, perfectly static one - see EvalContextTolerant's own
	// doc for why EvalContext itself cannot be used for that (it returns an
	// EMPTY context, not merely one missing the refused entry, the instant
	// any single reference fails validation).
	hclCtx, ctxDiags := scope.EvalContextTolerant(ctx, refs)
	diags = diags.Append(ctxDiags)

	val, evalDiags := expr.Value(hclCtx)
	diags = diags.Append(evalDiags)

	if diags.HasErrors() && onlyRefusedReferenceErrors(diags) &&
		val != cty.NilVal && !val.IsNull() && val.IsWhollyKnown() && !val.ContainsMarked() {
		return val, nil
	}

	return val, diags.ToHCL()
}

// onlyRefusedReferenceErrors reports whether every ERROR-severity
// diagnostic in diags carries a [RefusedReference] extra - attached only by
// [staticScopeData.StaticValidateReferences] - and that there is at least
// one such error. A diagnostic with no RefusedReference extra (a genuine
// parse failure, a type conversion error, an undefined variable) makes this
// false, which is the conservative direction: [StaticEvaluator.
// EvaluateStructural] only looks past a refusal it can identify as
// specifically "a reference was out of static scope", never past an error
// it cannot classify.
func onlyRefusedReferenceErrors(diags tfdiags.Diagnostics) bool {
	found := false
	for _, d := range diags {
		if d.Severity() != tfdiags.Error {
			continue
		}
		ref := tfdiags.ExtraInfo[RefusedReference](d)
		if ref.Subject == nil {
			return false
		}
		found = true
	}
	return found
}

func (s StaticEvaluator) DecodeExpression(ctx context.Context, expr hcl.Expression, ident StaticIdentifier, val any) hcl.Diagnostics {
	srcVal, diags := s.Evaluate(ctx, expr, ident)
	if diags.HasErrors() {
		return diags
	}

	if marks.Contains(srcVal, marks.Sensitive) {
		return diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Sensitive value not allowed",
			Detail:   fmt.Sprintf("Sensitive values, or values derived from sensitive values, cannot be used as %s.", ident.String()),
			Subject:  expr.Range().Ptr(),
		})
	}
	if marks.Contains(srcVal, marks.Ephemeral) {
		return diags.Append(&hcl.Diagnostic{
			Severity: hcl.DiagError,
			Summary:  "Ephemeral value not allowed",
			Detail:   fmt.Sprintf("Ephemeral values, or values derived from ephemeral values, cannot be used as %s.", ident.String()),
			Subject:  expr.Range().Ptr(),
		})
	}

	return diags.Extend(gohcl.DecodeValue(srcVal, expr.StartRange(), expr.Range(), val))
}

// DecodeBlock decodes body against spec, expanding any "dynamic" block it
// contains first - the same two-phase split stock OpenTofu's own plan-time
// evaluator uses (internal/lang/eval.go's Scope.ExpandBlock, then
// Scope.EvalBlock): phase one resolves whatever a "dynamic" block's own
// for_each/iterator/labels arguments reference and expands the block; phase
// two resolves the expanded body's own references and decodes it.
//
// Before this, DecodeBlock ran phase two alone, directly against the raw
// body. hcldec rejects a "dynamic" block outright ("Blocks of type
// \"dynamic\" are not expected here") because it is not a block type any
// schema ever declares, so a data source's arguments using one - an IAM
// policy document's `dynamic "statement" { ... }`, ordinary and common in
// real configurations - refused every time, never silently: the caller
// always got an error diagnostic, not a wrong or partial value. See
// internal/live/dataread's #212 fix, which is the concrete case that first
// exercised this.
func (s StaticEvaluator) DecodeBlock(ctx context.Context, body hcl.Body, spec hcldec.Spec, ident StaticIdentifier) (cty.Value, hcl.Diagnostics) {
	var diags hcl.Diagnostics

	expandRefs, expandRefsDiags := lang.References(addrs.ParseRef, dynblock.ExpandVariablesHCLDec(body, spec))
	diags = append(diags, expandRefsDiags.ToHCL()...)
	if diags.HasErrors() {
		return cty.DynamicVal, diags
	}

	expandCtx, expandCtxDiags := s.scope(ident).EvalContext(ctx, expandRefs)
	diags = append(diags, expandCtxDiags.ToHCL()...)
	if diags.HasErrors() {
		return cty.DynamicVal, diags
	}
	if expandCtx == nil {
		expandCtx = &hcl.EvalContext{}
	}
	body = dynblock.Expand(body, expandCtx)

	refs, refsDiags := lang.References(addrs.ParseRef, hcldec.Variables(body, spec))
	diags = append(diags, refsDiags.ToHCL()...)
	if diags.HasErrors() {
		return cty.DynamicVal, diags
	}

	hclCtx, ctxDiags := s.scope(ident).EvalContext(ctx, refs)
	diags = append(diags, ctxDiags.ToHCL()...)
	if diags.HasErrors() {
		return cty.DynamicVal, diags
	}

	val, valDiags := hcldec.Decode(body, spec, hclCtx)
	diags = append(diags, valDiags...)
	return val, diags
}

func (s StaticEvaluator) EvalContext(ctx context.Context, ident StaticIdentifier, refs []*addrs.Reference) (*hcl.EvalContext, hcl.Diagnostics) {
	return s.EvalContextWithParent(ctx, nil, ident, refs)
}

func (s StaticEvaluator) EvalContextWithParent(ctx context.Context, parent *hcl.EvalContext, ident StaticIdentifier, refs []*addrs.Reference) (*hcl.EvalContext, hcl.Diagnostics) {
	evalCtx, diags := s.scope(ident).EvalContextWithParent(ctx, parent, refs)
	return evalCtx, diags.ToHCL()
}
