// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

// Package docrefs is issue #256 item 8: internal/live's comments cite other
// symbols in Go's doc-link syntax - "[pkg.Symbol]" - and nothing checked that
// the citation still resolved. A comment can assert something about a symbol
// that no longer exists, which is the milder cousin of a defect this project
// has already shipped once: a comment cited providerscope.Resolve's doc for
// a guarantee that function never made, and it became a wrong rendered
// identity once a real value routed through it.
//
// This is the checkable half only. Whether the CLAIM a citation makes about
// the cited symbol is still true is not mechanically checkable - that needs
// a reader - and this package does not pretend otherwise: it only proves the
// symbol named still exists where the citation says it does.
//
// # What counts as a citation
//
// Go's doc comment syntax (https://go.dev/doc/comment#links) resolves
// "[pkg.Name]" using the CURRENT FILE's own import statements: the link only
// works if that file imports a package whose local identifier is pkg. This
// scanner uses the identical rule - a bracketed "pkg.Symbol" or
// "pkg.Type.Member" only counts as a citation into pkg when the file it
// appears in actually imports something named pkg - which is what keeps it
// from misreading prose that happens to contain a bracketed, dotted word as
// a broken reference. A single bracketed word with no dot ("[AtLeast]") is a
// same-package citation and out of scope: it cannot name a symbol that does
// not exist in the same file's own package without the package failing to
// build, so nothing here needs to check it separately.
//
// # Scope
//
// Every .go file under internal/live, source and test files both. A
// citation into a package outside this module (aws-sdk-go's s3.Client,
// hcl's hcl.Diagnostic) is skipped - there is no committed source to check
// it against, and the exact false positives issue #256's own scouting pass
// found were two of these (s3.Client, types.Info).
package docrefs

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/intentius/choudoufu/internal/live/flocitest"
)

// modulePath is this repository's own module path. A citation resolves to a
// checkable package only when its import path starts with this - anything
// else is a dependency this repository does not own the source of.
const modulePath = "github.com/intentius/choudoufu"

// citationRe matches a Go doc-link bracket whose content is one or more
// dot-separated identifiers: "[pkg.Symbol]", "[pkg.Type.Member]", or the
// same-package "[Symbol]" this package does not check further. It
// deliberately excludes a bracket immediately followed by "(", which is a
// Markdown link ("[text](url)"), not a Go doc link.
var citationRe = regexp.MustCompile(`\[([A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*)\](?:[^(]|$)`)

// pkgSymbols is one package's declared top-level names, plus its
// "Type.Member" pairs for methods and struct fields - the two shapes a
// multi-segment citation can name beyond a bare top-level identifier.
type pkgSymbols struct {
	names   map[string]bool
	members map[string]bool // "Type.Member" (method or struct field)
}

func (s *pkgSymbols) has(symbolPath string) bool {
	if s.names[symbolPath] {
		return true
	}
	return s.members[symbolPath]
}

// receiverTypeName strips the pointer star and any generic type parameter
// list from a method receiver's type expression, returning the plain type
// name a citation would name it by.
func receiverTypeName(expr ast.Expr) string {
	switch e := expr.(type) {
	case *ast.StarExpr:
		return receiverTypeName(e.X)
	case *ast.IndexExpr: // generic receiver, e.g. (s *Set[T])
		return receiverTypeName(e.X)
	case *ast.IndexListExpr:
		return receiverTypeName(e.X)
	case *ast.Ident:
		return e.Name
	default:
		return ""
	}
}

// loadPackageSymbols parses every .go file directly in dir (no recursion -
// Go packages are one directory each) and collects its declared names.
func loadPackageSymbols(dir string) (*pkgSymbols, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	syms := &pkgSymbols{names: map[string]bool{}, members: map[string]bool{}}
	fset := token.NewFileSet()
	found := false
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		found = true
		f, err := parser.ParseFile(fset, filepath.Join(dir, e.Name()), nil, 0)
		if err != nil {
			return nil, err
		}
		for _, decl := range f.Decls {
			switch d := decl.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil || len(d.Recv.List) == 0 {
					syms.names[d.Name.Name] = true
					continue
				}
				recv := receiverTypeName(d.Recv.List[0].Type)
				if recv != "" {
					syms.members[recv+"."+d.Name.Name] = true
				}
			case *ast.GenDecl:
				for _, spec := range d.Specs {
					switch s := spec.(type) {
					case *ast.TypeSpec:
						syms.names[s.Name.Name] = true
						switch t := s.Type.(type) {
						case *ast.StructType:
							if t.Fields != nil {
								for _, field := range t.Fields.List {
									for _, name := range field.Names {
										syms.members[s.Name.Name+"."+name.Name] = true
									}
								}
							}
						case *ast.InterfaceType:
							// An interface's methods are declared as fields
							// of the interface type, not as *ast.FuncDecl
							// with a receiver - the first version of this
							// scanner missed this entirely, which is exactly
							// the false-positive shape issue #256's own
							// scouting pass hit with its own crude parser.
							if t.Methods != nil {
								for _, field := range t.Methods.List {
									for _, name := range field.Names {
										syms.members[s.Name.Name+"."+name.Name] = true
									}
								}
							}
						}
					case *ast.ValueSpec:
						for _, name := range s.Names {
							syms.names[name.Name] = true
						}
					}
				}
			}
		}
	}
	if !found {
		return nil, os.ErrNotExist
	}
	return syms, nil
}

// citation is one resolved (or unresolved) doc-link found in the tree.
type citation struct {
	file       string // repo-relative
	raw        string // the bracket's full content, e.g. "lint.RuleStateBackend"
	importPath string
	symbolPath string
}

// TestGodocCitationsResolve is issue #256 item 8's guard. It does not check
// that a citation's surrounding claim is still accurate - only a reader can
// do that - but a citation naming a symbol that no longer exists at all is
// mechanically checkable, and until this test nothing did.
func TestGodocCitationsResolve(t *testing.T) {
	root := flocitest.RepoRoot(t)
	scanDir := filepath.Join(root, "internal", "live")

	var files []string
	if err := filepath.WalkDir(scanDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(p, ".go") {
			files = append(files, p)
		}
		return nil
	}); err != nil {
		t.Fatalf("walking %s: %v", scanDir, err)
	}
	if len(files) < 100 {
		t.Fatalf("found only %d .go files under %s; the walk is not reaching the tree it is supposed to cover, "+
			"so a green result here proves nothing", len(files), scanDir)
	}

	pkgCache := map[string]*pkgSymbols{} // import path -> symbols, or nil if unresolvable
	var citations []citation
	var misses []string
	var external, samePackage int

	for _, file := range files {
		fset := token.NewFileSet()
		astf, err := parser.ParseFile(fset, file, nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", file, err)
		}
		relFile, err := filepath.Rel(root, file)
		if err != nil {
			t.Fatalf("relativizing %s: %v", file, err)
		}
		relFile = filepath.ToSlash(relFile)

		imports := map[string]string{} // local identifier -> import path
		for _, imp := range astf.Imports {
			p, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				continue
			}
			local := path.Base(p)
			if imp.Name != nil {
				local = imp.Name.Name
			}
			imports[local] = p
		}

		for _, cg := range astf.Comments {
			text := cg.Text()
			for _, m := range citationRe.FindAllStringSubmatch(text, -1) {
				parts := strings.Split(m[1], ".")
				if len(parts) < 2 {
					samePackage++
					continue
				}
				pkgName, symbolPath := parts[0], strings.Join(parts[1:], ".")
				importPath, ok := imports[pkgName]
				if !ok {
					// Not a package this file imports under that identifier -
					// most commonly prose that happens to contain a
					// bracketed, dotted word ("[foo.bar]" as a literal
					// example) rather than a doc link. Go's own doc-link
					// resolution would not treat this as a link either.
					continue
				}
				if !strings.HasPrefix(importPath, modulePath) {
					external++
					continue
				}
				citations = append(citations, citation{
					file: relFile, raw: m[1], importPath: importPath, symbolPath: symbolPath,
				})
			}
		}
	}

	sort.Slice(citations, func(i, j int) bool {
		if citations[i].file != citations[j].file {
			return citations[i].file < citations[j].file
		}
		return citations[i].raw < citations[j].raw
	})

	distinct := map[string]bool{}
	for _, c := range citations {
		distinct[c.importPath+"."+c.symbolPath] = true

		syms, cached := pkgCache[c.importPath]
		if !cached {
			rel := strings.TrimPrefix(c.importPath, modulePath+"/")
			dir := filepath.Join(root, filepath.FromSlash(rel))
			var err error
			syms, err = loadPackageSymbols(dir)
			if err != nil {
				// A package this module's own go.mod claims to have, that
				// this scan cannot load, is a scanner defect, not a
				// citation defect - fail loudly rather than skip it, which
				// is exactly the blind-scanner shape CLAUDE.md warns about.
				t.Fatalf("%s: [%s] cites package %q, which this scanner could not load from %s: %v",
					c.file, c.raw, c.importPath, dir, err)
			}
			pkgCache[c.importPath] = syms
		}
		if !syms.has(c.symbolPath) {
			misses = append(misses, fmt.Sprintf("%s: [%s] - package %q has no %q",
				c.file, c.raw, c.importPath, c.symbolPath))
		}
	}

	t.Logf("swept %d .go files under internal/live: %d cross-package citations (%d distinct), %d same-package "+
		"citations out of scope, %d citations into a package outside this module skipped",
		len(files), len(citations), len(distinct), samePackage, external)

	if len(misses) > 0 {
		sort.Strings(misses)
		t.Errorf("%d godoc citation(s) name a symbol that does not exist in the cited package:\n%s",
			len(misses), strings.Join(misses, "\n"))
	}
}
