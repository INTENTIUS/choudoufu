// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// This file reads the azurerm provider's Go source with go/parser, one
// package at a time and on demand, for the azurerm tag facts
// (rmtags.go). It is the subset of tools/azurerm-id-gen's loader those
// facts need - registrations, then each resource's Create function and
// its `tags` schema - rather than an import of it, because that tool is a
// package main. The provider vendors its dependencies, so an import path
// resolves either inside the module or under vendor/; nothing is fetched.

type rmLoader struct {
	root   string
	module string
	fset   *token.FileSet
	pkgs   map[string]*rmPkg
}

type rmPkg struct {
	path    string
	name    string
	files   []*ast.File
	funcs   map[string]*ast.FuncDecl
	methods map[string]map[string]*ast.FuncDecl // receiver type -> method -> decl
	types   map[string]*ast.TypeSpec
	consts  map[string]ast.Expr
	fileOf  map[ast.Node]*ast.File
	imports map[*ast.File]map[string]string
	// tagFields are the struct fields declared `tfschema:"tags"`: the
	// typed SDK's config-model spelling of the tags argument.
	tagFields map[string]bool
}

func newRMLoader(root string) (*rmLoader, error) {
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
	return &rmLoader{root: root, module: module, fset: token.NewFileSet(), pkgs: map[string]*rmPkg{}}, nil
}

func (l *rmLoader) dirOf(importPath string) string {
	if importPath == l.module {
		return l.root
	}
	if strings.HasPrefix(importPath, l.module+"/") {
		return filepath.Join(l.root, filepath.FromSlash(strings.TrimPrefix(importPath, l.module+"/")))
	}
	return filepath.Join(l.root, "vendor", filepath.FromSlash(importPath))
}

// inModule reports whether a package is the provider's own code, the only
// code the create-path analysis follows calls into.
func (l *rmLoader) inModule(p *rmPkg) bool {
	return p != nil && strings.HasPrefix(p.path, l.module+"/internal/")
}

func (l *rmLoader) pos(n ast.Node) string {
	p := l.fset.Position(n.Pos())
	rel, err := filepath.Rel(l.root, p.Filename)
	if err != nil {
		rel = p.Filename
	}
	return filepath.ToSlash(rel) + ":" + strconv.Itoa(p.Line)
}

func (l *rmLoader) load(importPath string) *rmPkg {
	if p, ok := l.pkgs[importPath]; ok {
		return p
	}
	p := l.loadDir(importPath, l.dirOf(importPath))
	l.pkgs[importPath] = p
	return p
}

func (l *rmLoader) loadDir(importPath, dir string) *rmPkg {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	p := &rmPkg{
		path:      importPath,
		funcs:     map[string]*ast.FuncDecl{},
		methods:   map[string]map[string]*ast.FuncDecl{},
		types:     map[string]*ast.TypeSpec{},
		consts:    map[string]ast.Expr{},
		fileOf:    map[ast.Node]*ast.File{},
		imports:   map[*ast.File]map[string]string{},
		tagFields: map[string]bool{},
	}
	var names []string
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
			continue
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
				recv := rmRecvTypeName(d.Recv.List[0].Type)
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
						if st, ok := s.Type.(*ast.StructType); ok {
							for _, fld := range st.Fields.List {
								if fld.Tag == nil {
									continue
								}
								tag, _ := strconv.Unquote(fld.Tag.Value)
								if reflect.StructTag(tag).Get("tfschema") == "tags" {
									for _, id := range fld.Names {
										p.tagFields[id.Name] = true
									}
								}
							}
						}
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

func (l *rmLoader) importNames(f *ast.File) map[string]string {
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

func (l *rmLoader) packageName(importPath string) string {
	if p, ok := l.pkgs[importPath]; ok && p != nil {
		return p.name
	}
	dir := l.dirOf(importPath)
	if entries, err := os.ReadDir(dir); err == nil {
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

func rmRecvTypeName(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.StarExpr:
		return rmRecvTypeName(t.X)
	case *ast.Ident:
		return t.Name
	case *ast.IndexExpr:
		return rmRecvTypeName(t.X)
	case *ast.IndexListExpr:
		return rmRecvTypeName(t.X)
	}
	return ""
}

// rmRegistration is one managed resource type and the declarations its
// facts are read from.
type rmRegistration struct {
	Type string
	Pkg  *rmPkg
	File *ast.File
	// Ctor is an untyped resource's constructor; TypeName a typed
	// resource's struct type.
	Ctor     *ast.FuncDecl
	TypeName string
}

// registrations returns every resource type registered by a
// SupportedResources() map (untyped SDK) or a Resources() slice (typed
// SDK) under internal/services, first registration wins.
func (l *rmLoader) registrations() ([]rmRegistration, error) {
	base := filepath.Join(l.root, "internal", "services")
	var dirs []string
	err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if d.Name() == "testdata" {
				return filepath.SkipDir
			}
			dirs = append(dirs, p)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(dirs)
	seen := map[string]bool{}
	var out []rmRegistration
	for _, dir := range dirs {
		rel, _ := filepath.Rel(l.root, dir)
		p := l.load(l.module + "/" + filepath.ToSlash(rel))
		if p == nil {
			continue
		}
		for _, f := range p.files {
			for _, d := range f.Decls {
				fd, ok := d.(*ast.FuncDecl)
				if !ok || fd.Recv == nil || fd.Body == nil {
					continue
				}
				var regs []rmRegistration
				switch fd.Name.Name {
				case "SupportedResources":
					regs = l.untypedRegistrations(p, f, fd)
				case "Resources":
					regs = l.typedRegistrations(p, f, fd)
				default:
					continue
				}
				for _, r := range regs {
					if !seen[r.Type] {
						seen[r.Type] = true
						out = append(out, r)
					}
				}
			}
		}
	}
	return out, nil
}

func rmRegisteredLit(fd *ast.FuncDecl) *ast.CompositeLit {
	var lit *ast.CompositeLit
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if lit != nil {
			return false
		}
		if cl, ok := n.(*ast.CompositeLit); ok {
			switch cl.Type.(type) {
			case *ast.ArrayType, *ast.MapType:
				lit = cl
				return false
			}
		}
		return true
	})
	return lit
}

func (l *rmLoader) untypedRegistrations(p *rmPkg, f *ast.File, fd *ast.FuncDecl) []rmRegistration {
	lit := rmRegisteredLit(fd)
	if lit == nil {
		return nil
	}
	var out []rmRegistration
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		name, ok := rmStringLit(kv.Key)
		if !ok || !strings.HasPrefix(name, "azurerm_") {
			continue
		}
		r := rmRegistration{Type: name}
		if call, ok := kv.Value.(*ast.CallExpr); ok {
			r.Pkg, r.File, r.Ctor = l.funcRef(p, f, call.Fun)
		}
		out = append(out, r)
	}
	return out
}

func (l *rmLoader) typedRegistrations(p *rmPkg, f *ast.File, fd *ast.FuncDecl) []rmRegistration {
	lit := rmRegisteredLit(fd)
	if lit == nil {
		return nil
	}
	var out []rmRegistration
	for _, e := range lit.Elts {
		tp, tname := l.typedElement(p, f, e)
		if tp == nil || tname == "" {
			continue
		}
		name := l.resourceTypeName(tp, tname)
		if !strings.HasPrefix(name, "azurerm_") {
			continue
		}
		out = append(out, rmRegistration{Type: name, Pkg: tp, File: tp.fileOf[tp.types[tname]], TypeName: tname})
	}
	return out
}

func (l *rmLoader) typedElement(p *rmPkg, f *ast.File, e ast.Expr) (*rmPkg, string) {
	switch x := e.(type) {
	case *ast.UnaryExpr:
		return l.typedElement(p, f, x.X)
	case *ast.CompositeLit:
		return l.typeRef(p, f, x.Type)
	case *ast.CallExpr:
		cp, cf, fd := l.funcRef(p, f, x.Fun)
		if fd == nil || fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
			return nil, ""
		}
		return l.typeRef(cp, cf, fd.Type.Results.List[0].Type)
	}
	return nil, ""
}

func (l *rmLoader) resourceTypeName(p *rmPkg, tname string) string {
	mp, m := l.methodWithEmbedding(p, tname, "ResourceType", 0)
	if m == nil || m.Body == nil {
		return ""
	}
	for _, s := range m.Body.List {
		rs, ok := s.(*ast.ReturnStmt)
		if !ok || len(rs.Results) != 1 {
			continue
		}
		if s, ok := rmStringLit(rs.Results[0]); ok {
			return s
		}
		if id, ok := rs.Results[0].(*ast.Ident); ok {
			if s, ok := rmStringLit(mp.consts[id.Name]); ok {
				return s
			}
		}
	}
	return ""
}

func (l *rmLoader) methodWithEmbedding(p *rmPkg, tname, method string, depth int) (*rmPkg, *ast.FuncDecl) {
	if m := p.methods[tname][method]; m != nil {
		return p, m
	}
	if depth >= 3 {
		return nil, nil
	}
	ts := p.types[tname]
	if ts == nil {
		return nil, nil
	}
	st, ok := ts.Type.(*ast.StructType)
	if !ok {
		return nil, nil
	}
	for _, fld := range st.Fields.List {
		if len(fld.Names) != 0 {
			continue
		}
		ep, en := l.typeRef(p, p.fileOf[ts], fld.Type)
		if ep == nil {
			continue
		}
		if mp, m := l.methodWithEmbedding(ep, en, method, depth+1); m != nil {
			return mp, m
		}
	}
	return nil, nil
}

func (l *rmLoader) funcRef(p *rmPkg, f *ast.File, e ast.Expr) (*rmPkg, *ast.File, *ast.FuncDecl) {
	switch x := e.(type) {
	case *ast.Ident:
		if fd := p.funcs[x.Name]; fd != nil {
			return p, p.fileOf[fd], fd
		}
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return nil, nil, nil
		}
		ip, ok := p.imports[f][id.Name]
		if !ok {
			return nil, nil, nil
		}
		op := l.load(ip)
		if op == nil {
			return nil, nil, nil
		}
		if fd := op.funcs[x.Sel.Name]; fd != nil {
			return op, op.fileOf[fd], fd
		}
	}
	return nil, nil, nil
}

func (l *rmLoader) typeRef(p *rmPkg, f *ast.File, e ast.Expr) (*rmPkg, string) {
	switch x := e.(type) {
	case *ast.StarExpr:
		return l.typeRef(p, f, x.X)
	case *ast.IndexExpr:
		return l.typeRef(p, f, x.X)
	case *ast.IndexListExpr:
		return l.typeRef(p, f, x.X)
	case *ast.Ident:
		if _, ok := p.types[x.Name]; ok {
			return p, x.Name
		}
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return nil, ""
		}
		ip, ok := p.imports[f][id.Name]
		if !ok {
			return nil, ""
		}
		op := l.load(ip)
		if op == nil {
			return nil, ""
		}
		if _, ok := op.types[x.Sel.Name]; ok {
			return op, x.Sel.Name
		}
	}
	return nil, ""
}

func rmStringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok || bl.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	return s, err == nil
}
