// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// loader parses the azurerm source one Go package at a time, on demand.
// The provider vendors its dependencies, so an import path resolves either
// inside the module (internal/...) or under vendor/; nothing is fetched and
// no go toolchain is invoked, which keeps the generator a pure function of
// the checkout it is pointed at.
type loader struct {
	root   string // the azurerm checkout
	module string // its module path, from go.mod
	fset   *token.FileSet
	pkgs   map[string]*pkg // by import path; nil value means "not found"
}

// pkg is one parsed package: its declarations indexed the way the
// resolver asks for them.
type pkg struct {
	path    string
	dir     string
	name    string
	files   []*ast.File
	funcs   map[string]*ast.FuncDecl
	methods map[string]map[string]*ast.FuncDecl // receiver type -> method -> decl
	types   map[string]*ast.TypeSpec
	consts  map[string]ast.Expr // package-level const/var with a single value
	fileOf  map[ast.Node]*ast.File
	imports map[*ast.File]map[string]string // local name -> import path
}

func newLoader(root string) (*loader, error) {
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil, err
	}
	module := ""
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "module ") {
			module = strings.TrimSpace(strings.TrimPrefix(line, "module "))
			break
		}
	}
	return &loader{root: root, module: module, fset: token.NewFileSet(), pkgs: map[string]*pkg{}}, nil
}

// dirOf maps an import path to the directory holding it in the checkout.
func (l *loader) dirOf(importPath string) string {
	if importPath == l.module {
		return l.root
	}
	if strings.HasPrefix(importPath, l.module+"/") {
		return filepath.Join(l.root, filepath.FromSlash(strings.TrimPrefix(importPath, l.module+"/")))
	}
	return filepath.Join(l.root, "vendor", filepath.FromSlash(importPath))
}

// relPath is a file's path relative to the checkout, slash-separated: the
// spelling the artifact records, so it does not depend on where the
// checkout sits.
func (l *loader) relPath(file string) string {
	rel, err := filepath.Rel(l.root, file)
	if err != nil {
		return file
	}
	return filepath.ToSlash(rel)
}

// load parses the package at importPath, or returns nil when the checkout
// does not hold it (a standard-library package, say).
func (l *loader) load(importPath string) *pkg {
	if p, ok := l.pkgs[importPath]; ok {
		return p
	}
	p := l.loadDir(importPath, l.dirOf(importPath))
	l.pkgs[importPath] = p
	return p
}

func (l *loader) loadDir(importPath, dir string) *pkg {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	p := &pkg{
		path:    importPath,
		dir:     dir,
		funcs:   map[string]*ast.FuncDecl{},
		methods: map[string]map[string]*ast.FuncDecl{},
		types:   map[string]*ast.TypeSpec{},
		consts:  map[string]ast.Expr{},
		fileOf:  map[ast.Node]*ast.File{},
		imports: map[*ast.File]map[string]string{},
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		f, err := parser.ParseFile(l.fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		if p.name == "" {
			p.name = f.Name.Name
		}
		if f.Name.Name != p.name {
			continue // a stray package main, say
		}
		p.files = append(p.files, f)
		p.imports[f] = l.importNames(f)
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				p.fileOf[d] = f
				if d.Recv == nil || len(d.Recv.List) == 0 {
					p.funcs[d.Name.Name] = d
					continue
				}
				recv := recvTypeName(d.Recv.List[0].Type)
				if p.methods[recv] == nil {
					p.methods[recv] = map[string]*ast.FuncDecl{}
				}
				p.methods[recv][d.Name.Name] = d
			case *ast.GenDecl:
				for _, s := range d.Specs {
					switch s := s.(type) {
					case *ast.TypeSpec:
						p.types[s.Name.Name] = s
						p.fileOf[s] = f
					case *ast.ValueSpec:
						for i, id := range s.Names {
							if i < len(s.Values) {
								p.consts[id.Name] = s.Values[i]
							}
						}
					}
				}
			}
		}
	}
	if len(p.files) == 0 {
		return nil
	}
	return p
}

// importNames maps each import's local name to its path. An unnamed import
// is keyed by the package clause of the imported directory when the
// checkout holds it, and by the last path element otherwise; go-azure-sdk's
// API-version directories ("2023-05-01/storageaccounts") make the last
// element the right guess almost always, and the package clause settles
// the rest ("go-azure-helpers/lang/pointer" and friends).
func (l *loader) importNames(f *ast.File) map[string]string {
	out := map[string]string{}
	for _, imp := range f.Imports {
		ip, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if imp.Name != nil {
			out[imp.Name.Name] = ip
			continue
		}
		out[l.packageName(ip)] = ip
	}
	return out
}

// packageName reads only the package clause of the first Go file in the
// imported directory, so naming an import never parses a whole package.
func (l *loader) packageName(importPath string) string {
	if p, ok := l.pkgs[importPath]; ok && p != nil {
		return p.name
	}
	dir := l.dirOf(importPath)
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
				continue
			}
			f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, n), nil, parser.PackageClauseOnly)
			if err == nil && f.Name.Name != "main" {
				return f.Name.Name
			}
		}
	}
	base := path.Base(importPath)
	if len(base) > 1 && base[0] == 'v' && strings.Trim(base[1:], "0123456789") == "" {
		base = path.Base(path.Dir(importPath))
	}
	return strings.ReplaceAll(strings.TrimPrefix(base, "go-"), "-", "")
}

func recvTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return recvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return recvTypeName(t.X)
	}
	return ""
}

// method finds a method on a named type, whether declared on the value or
// the pointer receiver (loadDir indexes both under the bare name).
func (p *pkg) method(typeName, name string) *ast.FuncDecl {
	if m := p.methods[typeName]; m != nil {
		return m[name]
	}
	return nil
}

// uniqueMethod finds the one method named name declared on any type of
// the package, or nil when none or several are.
func (p *pkg) uniqueMethod(name string) *ast.FuncDecl {
	var found *ast.FuncDecl
	for _, ms := range p.methods {
		if m := ms[name]; m != nil {
			if found != nil {
				return nil
			}
			found = m
		}
	}
	return found
}
