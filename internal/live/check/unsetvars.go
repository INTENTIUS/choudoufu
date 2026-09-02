// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package check

import (
	"path/filepath"
	"regexp"
	"sort"

	"github.com/hashicorp/hcl/v2"
	"github.com/hashicorp/hcl/v2/hclsyntax"

	"github.com/intentius/choudoufu/internal/configs"
)

// This file is issue #161. A live-check run against a configuration whose
// required input variables have no value reports refusals that are artifacts
// of the missing values rather than facts about the configuration. Measured
// on an AFT account-customization fixture: 2 refusals across 4 sites with no
// tfvars, 1 across 2 with them.
//
// The report already said so, in a trailer after the verdict, the findings
// and two other paragraphs. That is honest and useless in the same breath -
// someone assessing compatibility reads the headline and stops, and the
// headline was wrong in the direction that makes this fork look worse than
// it is. It matters most for the audiences worth winning, because anyone
// whose variables come from a pipeline (AFT, Terragrunt, CI) runs with none
// of them set.
//
// Attribution is per site rather than per report, so the caveat can be
// attached to the refusals it actually explains instead of hanging over all
// of them. A configuration with one variable-dependent refusal and three
// real ones should say exactly that.

// AttributeUnsetVariables marks every site whose own source text references
// a required input variable that had no value, and returns how many it
// marked.
//
// It runs after Analyze rather than inside it, and must: the static
// evaluator is lazy, so most variables are first read during identity
// resolution and the unset set is not complete until the analysis has
// finished. See [LoadResult.UnsetVariables].
//
// What it reads is the refusal's own range - the offending construct as the
// author wrote it - and not the whole file. A refusal on line 40 is not
// excused by an unset variable used on line 12.
func (r *Report) AttributeUnsetVariables(unset []string, sources map[string]*hcl.File) int {
	if len(unset) == 0 || len(sources) == 0 {
		return 0
	}
	want := make(map[string]bool, len(unset))
	for _, name := range unset {
		want[name] = true
	}

	// r.Load is set by both front ends (LiveCheckCommand.liveCheck and
	// tools/corpus-gen) before this runs, and by [Dir] and the test helper
	// alike - see the doc comment on [unsetRefsAt] for why a module tree is
	// needed at all. A caller that never set it (only a hand-built Report in
	// a test, none of which exercise this path) simply gets the site-local
	// behavior this function has always had: dirIndex is nil, and every
	// module lookup below falls through as if the site were in the root
	// module, exactly like before this walk existed.
	dirIndex := moduleDirIndex(r.Load.Config)

	marked := 0
	for i := range r.Findings {
		f := &r.Findings[i]
		seen := map[string]bool{}
		for j := range f.Sites {
			site := &f.Sites[j]
			refs := unsetRefsAt(site, want, sources, dirIndex)
			if len(refs) == 0 {
				continue
			}
			site.UnsetVarRefs = refs
			marked++
			for _, name := range refs {
				seen[name] = true
			}
		}
		f.UnsetVarRefs = sortedKeys(seen)
		f.UnsetVarSites = countMarked(f.Sites)
	}
	return marked
}

// unsetRefsAt returns the unset ROOT variables this site actually depends
// on, following the site's own references across local definitions and
// module-call arguments to wherever they ultimately name a root variable.
//
// This exists because a site's own text can name a variable that is not a
// root variable at all: a child module's `bucket = var.name` reads that
// module's OWN declared "name" variable, whose value is supplied by the
// module call's argument expression back in the parent - which might itself
// be `name = local.env`, and that local might read a root variable several
// hops further up. Matching "var.name" against the root unset set directly
// (the original #161 implementation) finds nothing here, even though
// supplying the one root variable at the top of the chain would clear the
// refusal. See #178's "unresolvable identity" scoping comment for the
// mobile-backend case this generalizes from.
//
// The walk is general: it knows nothing about any particular estate, module,
// or resource type. It only knows two shapes, both drawn from the config
// tree already loaded for identity resolution:
//
//   - "local.x" inside a module: chase local.Expr in the SAME module.
//   - "var.x" inside a NON-ROOT module: chase the module call's own
//     argument expression for "x", evaluated in the PARENT module - the
//     same relationship [resolver.namedDef] in internal/live/identity
//     follows for resolution, mirrored here for measurement only.
//
// A "var.x" reference in the root module (or when dirIndex has no module
// tree to consult at all) names a root variable directly, which is the
// original behavior this preserves exactly when there is nothing to chase.
//
// Two readings for the SITE's own range, and the fallback is not decoration.
// Parsing the range as an expression is exact: it finds var.x inside a
// "${...}" interpolation, and it does not match the word "var" in a comment
// or a string. But a refusal's range is not always a standalone expression -
// lint rules point at block headers and whole resource blocks - and
// hclsyntax refuses those. Rather than attribute nothing there, the fallback
// scans the same bytes textually. Every hop AFTER the site's own range reads
// an hcl.Expression the config loader already parsed successfully (a
// local's or a module call argument's Expr), so no such fallback is needed
// there.
//
// Both are bounded by the same range, so the fallback's looseness costs at
// most a false positive inside that one construct, and a false positive here
// weakens a caveat rather than inventing a refusal - this function only ever
// adds to the accounting, never to a refusal outcome.
func unsetRefsAt(site *Site, want map[string]bool, sources map[string]*hcl.File, dirIndex map[string][]*configs.Config) []string {
	if site.File == "" || site.EndByte <= site.StartByte {
		return nil
	}
	file, ok := sources[site.File]
	if !ok || site.EndByte > len(file.Bytes) {
		return nil
	}
	src := file.Bytes[site.StartByte:site.EndByte]

	var vars0, locals0 []string
	expr, diags := hclsyntax.ParseExpression(src, site.File, hcl.Pos{
		Line: site.Line, Column: site.Column, Byte: site.StartByte,
	})
	if !diags.HasErrors() && expr != nil {
		vars0, locals0 = exprRefs(expr)
	} else {
		vars0, locals0 = textRefs(src)
	}

	found := map[string]bool{}
	mods := modulesAt(dirIndex, site.File)
	if len(mods) == 0 {
		// No config tree to consult (or no module matched this file): fall
		// through to the original, module-unaware behavior of matching a
		// direct var.x reference against the root unset set.
		chaseRefs(nil, vars0, locals0, want, found, map[chainStep]bool{}, 0)
		return sortedKeys(found)
	}
	for _, mod := range mods {
		chaseRefs(mod, vars0, locals0, want, found, map[chainStep]bool{}, 0)
	}
	return sortedKeys(found)
}

// maxUnsetVarChaseDepth bounds how many locals or module-call arguments this
// walk will follow before giving up. It mirrors maxStaticDecomposeDepth in
// internal/live/identity/localvalue.go: the same shape of chain, the same
// reason a bound exists (a pathological or self-referential chain becomes a
// clean stop rather than a stack overflow; ordinary configurations are two
// or three levels deep at most), kept as a separate constant because this
// package does not import identity's unexported one.
const maxUnsetVarChaseDepth = 16

// chainStep de-duplicates one hop of the walk so a diamond-shaped or
// self-referential chain (two locals both reading a third) is visited once
// per module, not re-expanded exponentially.
type chainStep struct {
	mod  *configs.Config
	kind string // "var" or "local"
	name string
}

// chaseRefs follows var.x and local.x names found at one hop to whatever
// root variables they ultimately depend on, recording any that are in want.
// mod is the module the names were read in; nil means "no module context",
// which is treated as the root (see [unsetRefsAt]).
func chaseRefs(mod *configs.Config, vars, locals []string, want map[string]bool, found map[string]bool, visited map[chainStep]bool, depth int) {
	if depth > maxUnsetVarChaseDepth {
		return
	}

	for _, name := range vars {
		if mod == nil || len(mod.Path) == 0 {
			// Root module (or no module context at all): var.x names a
			// root variable directly, the original #161 behavior.
			if want[name] {
				found[name] = true
			}
			continue
		}

		step := chainStep{mod, "var", name}
		if visited[step] {
			continue
		}
		visited[step] = true

		parent := mod.Parent
		if parent == nil || parent.Module == nil {
			continue
		}
		callName := mod.Path[len(mod.Path)-1]
		mc, ok := parent.Module.ModuleCalls[callName]
		if !ok || mc.Config == nil {
			continue
		}
		attrs, attrDiags := mc.Config.JustAttributes()
		if attrDiags.HasErrors() {
			continue
		}
		attr, ok := attrs[name]
		if !ok {
			// No argument was given for this variable in the module block -
			// it takes its own declared default, which is
			// configuration-authored, never a root variable. Nothing to
			// chase.
			continue
		}
		pv, pl := exprRefs(attr.Expr)
		chaseRefs(parent, pv, pl, want, found, visited, depth+1)
	}

	for _, name := range locals {
		if mod == nil || mod.Module == nil {
			continue
		}

		step := chainStep{mod, "local", name}
		if visited[step] {
			continue
		}
		visited[step] = true

		local, ok := mod.Module.Locals[name]
		if !ok {
			continue
		}
		lv, ll := exprRefs(local.Expr)
		chaseRefs(mod, lv, ll, want, found, visited, depth+1)
	}
}

// exprRefs reads the var.x and local.x two-step references an already
// parsed hcl.Expression makes, split by root. Unlike [textRefs] this needs
// no fallback: every caller here holds an Expr the config loader already
// parsed successfully.
func exprRefs(expr hcl.Expression) (vars, locals []string) {
	if expr == nil {
		return nil, nil
	}
	varsSeen, localsSeen := map[string]bool{}, map[string]bool{}
	for _, traversal := range expr.Variables() {
		if len(traversal) < 2 {
			continue
		}
		step, ok := traversal[1].(hcl.TraverseAttr)
		if !ok {
			continue
		}
		switch traversal.RootName() {
		case "var":
			varsSeen[step.Name] = true
		case "local":
			localsSeen[step.Name] = true
		}
	}
	return sortedKeys(varsSeen), sortedKeys(localsSeen)
}

// textRefs is [unsetRefsAt]'s fallback for a range hclsyntax refuses to
// parse as a standalone expression - a lint rule's block header or whole
// resource block. It scans the same bytes for var.x and local.x by pattern
// rather than by parse tree.
func textRefs(src []byte) (vars, locals []string) {
	varsSeen, localsSeen := map[string]bool{}, map[string]bool{}
	for _, m := range varRefRe.FindAllSubmatch(src, -1) {
		varsSeen[string(m[1])] = true
	}
	for _, m := range localRefRe.FindAllSubmatch(src, -1) {
		localsSeen[string(m[1])] = true
	}
	return sortedKeys(varsSeen), sortedKeys(localsSeen)
}

// varRefRe matches a root input variable reference in source text.
var varRefRe = regexp.MustCompile(`\bvar\.([A-Za-z0-9_-]+)`)

// localRefRe matches a local value reference in source text.
var localRefRe = regexp.MustCompile(`\blocal\.([A-Za-z0-9_-]+)`)

// moduleDirIndex maps each module's source directory to the config tree
// node(s) loaded from it, so [modulesAt] can find a site's module from the
// filename a refusal already carries - the config tree has no direct
// "which module is this file in" lookup, only the filesystem directory each
// module was loaded from ([configs.Module.SourceDir]).
//
// More than one node can share a key when two module calls source the same
// directory; [modulesAt] chases every one of them rather than guessing,
// which can only add a false-positive attribution in that case, never
// change a refusal outcome.
func moduleDirIndex(cfg *configs.Config) map[string][]*configs.Config {
	idx := map[string][]*configs.Config{}
	var walk func(c *configs.Config)
	walk = func(c *configs.Config) {
		if c == nil || c.Module == nil {
			return
		}
		dir := filepath.Clean(c.Module.SourceDir)
		idx[dir] = append(idx[dir], c)
		for _, child := range c.Children {
			walk(child)
		}
	}
	walk(cfg)
	return idx
}

// modulesAt finds the config tree node(s) a source filename belongs to, by
// the directory it was loaded from. Empty when dirIndex is nil (no config
// tree was available to this report) or the file matches no known module -
// both are the "fall back to the original behavior" case in [unsetRefsAt].
func modulesAt(dirIndex map[string][]*configs.Config, filename string) []*configs.Config {
	if len(dirIndex) == 0 || filename == "" {
		return nil
	}
	return dirIndex[filepath.Clean(filepath.Dir(filename))]
}

func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func countMarked(sites []Site) int {
	n := 0
	for _, s := range sites {
		if len(s.UnsetVarRefs) > 0 {
			n++
		}
	}
	return n
}
