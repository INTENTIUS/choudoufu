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
	"github.com/hashicorp/hcl/v2/gohcl"
	"github.com/hashicorp/hcl/v2/hcldec"
	"github.com/intentius/choudoufu/internal/addrs"
	"github.com/intentius/choudoufu/internal/instances"
	"github.com/intentius/choudoufu/internal/lang"
	"github.com/intentius/choudoufu/internal/lang/marks"
	"github.com/zclconf/go-cty/cty"
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
// everything else keeps refusing exactly as before. Managed-mode references
// are never answered this way: there is nothing a pre-plan read could
// honestly say about an object the plan may be about to change.
func (s *StaticEvaluator) WithDataResults(lookup StaticDataLookup) *StaticEvaluator {
	if s == nil || lookup == nil {
		return s
	}
	dup := *s
	dup.dataLookup = lookup
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

func (s StaticEvaluator) DecodeBlock(ctx context.Context, body hcl.Body, spec hcldec.Spec, ident StaticIdentifier) (cty.Value, hcl.Diagnostics) {
	var diags hcl.Diagnostics

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
