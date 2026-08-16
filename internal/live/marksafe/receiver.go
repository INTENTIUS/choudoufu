// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package marksafe

import (
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/tools/go/packages"
)

// ctyValueType is the only receiver type this check is about, spelled the way
// the Go type checker spells it.
const ctyValueType = "github.com/zclconf/go-cty/cty.Value"

// ReceiverIndex maps a selector's source position to the type of the value it
// is selected from, as resolved by the Go type checker. An absent position
// means the type is unknown, never that it is safe - see [ProofNotCtyValue].
//
// Keyed by position rather than by expression text because two calls in one
// function can write the same receiver name for different values, and because
// the index is built from a different token.FileSet than [Scan] parses with.
// Line and column of the same byte in the same file agree across FileSets;
// token.Pos values do not.
type ReceiverIndex map[string]string

// normPath makes the two sides of the index agree on how a file is spelled.
// [Scan] is handed relative directories and go/packages reports absolute
// ones, and either can arrive through a symlinked checkout - the trap that
// costs this repository ten false test failures whenever PWD is left set. A
// disagreement here would show up as an unresolved receiver, which is
// treated as unproven, so it fails loudly rather than silently; normalising
// both sides keeps it from failing at all.
//
// Memoized per distinct path because it runs once per selector expression,
// of which these packages have tens of thousands.
var normCache sync.Map // string -> string

func normPath(path string) string {
	if v, ok := normCache.Load(path); ok {
		return v.(string)
	}
	out := path
	if abs, err := filepath.Abs(path); err == nil {
		out = abs
	}
	if real, err := filepath.EvalSymlinks(out); err == nil {
		out = real
	}
	normCache.Store(path, out)
	return out
}

func receiverKey(path string, pos token.Position) string {
	return fmt.Sprintf("%s:%d:%d", normPath(path), pos.Line, pos.Column)
}

// LoadReceiverIndex type-checks the packages matching patterns, rooted at
// moduleDir, and records the receiver type of every selector expression in
// them.
//
// Every selector is recorded, not only those whose name is currently unsafe.
// Filtering here would mean the index went stale the moment cty grew another
// unsafe accessor, and would reintroduce the coupling this package spends its
// derivation avoiding.
func LoadReceiverIndex(moduleDir string, patterns ...string) (ReceiverIndex, error) {
	cfg := &packages.Config{
		Mode: packages.NeedName | packages.NeedFiles | packages.NeedSyntax |
			packages.NeedTypes | packages.NeedTypesInfo,
		Dir: moduleDir,
	}
	pkgs, err := packages.Load(cfg, patterns...)
	if err != nil {
		return nil, fmt.Errorf("marksafe: type-checking %v: %w", patterns, err)
	}
	if len(pkgs) == 0 {
		return nil, fmt.Errorf("marksafe: %v matched no packages", patterns)
	}

	out := ReceiverIndex{}
	var failed []string
	for _, p := range pkgs {
		if len(p.Errors) > 0 {
			failed = append(failed, fmt.Sprintf("%s: %v", p.PkgPath, p.Errors[0]))
			continue
		}
		if p.TypesInfo == nil {
			failed = append(failed, p.PkgPath+": no type information")
			continue
		}
		fset := p.Fset
		for _, f := range p.Syntax {
			ast.Inspect(f, func(n ast.Node) bool {
				sel, ok := n.(*ast.SelectorExpr)
				if !ok {
					return true
				}
				tv, ok := p.TypesInfo.Types[sel.X]
				if !ok || tv.Type == nil {
					return true
				}
				pos := fset.Position(sel.Sel.Pos())
				if !pos.IsValid() {
					return true
				}
				out[receiverKey(pos.Filename, pos)] = typeName(tv.Type)
				return true
			})
		}
	}
	if len(failed) > 0 {
		return nil, fmt.Errorf("marksafe: %d package(s) would not type-check, so no receiver can be resolved and every site would be reported unproven; first: %s",
			len(failed), failed[0])
	}
	return out, nil
}

// typeName renders a type the way this check compares them: pointers and
// aliases followed to the named type underneath, because a method on
// cty.Value is reached through a *cty.Value receiver too.
func typeName(t types.Type) string {
	for {
		ptr, ok := t.(*types.Pointer)
		if !ok {
			break
		}
		t = ptr.Elem()
	}
	return types.TypeString(t, func(p *types.Package) string { return p.Path() })
}

// isCtyValue reports whether a resolved receiver type is cty.Value.
func isCtyValue(name string) bool {
	return name == ctyValueType || strings.HasPrefix(name, ctyValueType+"[")
}
