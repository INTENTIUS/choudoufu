// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"go/ast"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// registration is one managed resource type as the provider registers it,
// with the expression that validates its import ID: the Importer of an
// untyped (pluginsdk.Resource) resource, or the IDValidationFunc of a typed
// (sdk.Resource) one. Those two are the provider's own statement of which
// ID it accepts, which is why this generator reads them and nothing else.
type registration struct {
	Type    string
	Pkg     *pkg
	File    *ast.File
	Via     string   // "Importer" or "IDValidationFunc"
	Expr    ast.Node // the importer value, or the IDValidationFunc body
	Problem string   // set when no validating expression was found

	// ReadPkg, ReadFile and Read are the resource's Read function: the
	// untyped resource's `Read: f` (f's declaration), or the typed
	// resource's Read() method. Its body parses the stored id, d.Id() or
	// metadata.ResourceData.Id(), which is the id the resource actually
	// holds; [loader.readIDTypeOf] reads it to check the importer against
	// (#1120 unit 13). Nil when no Read was found.
	ReadPkg  *pkg
	ReadFile *ast.File
	Read     ast.Node

	// CreatePkg, CreateFile and Create are the resource's Create function,
	// found as Read is: an untyped resource's `Create: f`, a typed
	// resource's Create() method (whose body holds the Func literal).
	// [loader.createCallSources] reads the id it builds. Nil when none was
	// found.
	CreatePkg  *pkg
	CreateFile *ast.File
	Create     ast.Node
}

// registrations walks every package under internal/services and returns
// each resource type registered by a SupportedResources() map (untyped
// SDK) or a Resources() slice (typed SDK). A type registered twice keeps
// its first registration in path order and the duplicate is ignored; the
// provider itself would fail to start on a real duplicate.
func (l *loader) registrations() ([]registration, error) {
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
	var out []registration
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
				var regs []registration
				switch fd.Name.Name {
				case "SupportedResources":
					regs = l.untypedRegistrations(p, f, fd)
				case "Resources":
					regs = l.typedRegistrations(p, f, fd)
				default:
					continue
				}
				for _, r := range regs {
					if seen[r.Type] {
						continue
					}
					seen[r.Type] = true
					out = append(out, r)
				}
			}
		}
	}
	return out, nil
}

// registeredLit is the slice or map literal a registration method builds:
// returned directly, or assigned to a local that is appended to and then
// returned (containers' Resources() appends its auto-registered half).
func registeredLit(fd *ast.FuncDecl) *ast.CompositeLit {
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

func (l *loader) untypedRegistrations(p *pkg, f *ast.File, fd *ast.FuncDecl) []registration {
	lit := registeredLit(fd)
	if lit == nil {
		return nil
	}
	var out []registration
	for _, e := range lit.Elts {
		kv, ok := e.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		bl, ok := kv.Key.(*ast.BasicLit)
		if !ok {
			continue
		}
		name, err := strconv.Unquote(bl.Value)
		if err != nil || !strings.HasPrefix(name, "azurerm_") {
			continue
		}
		r := registration{Type: name, Via: "Importer"}
		call, ok := kv.Value.(*ast.CallExpr)
		if !ok {
			r.Problem = "registration value is not a constructor call"
			out = append(out, r)
			continue
		}
		cp, cf, ctor := l.funcRef(p, f, call.Fun)
		if ctor == nil {
			r.Problem = "resource constructor not found in source"
			out = append(out, r)
			continue
		}
		ip, ifl, expr := l.findImporter(cp, cf, ctor, 0, map[*ast.FuncDecl]bool{})
		if expr == nil {
			r.Problem = "resource declares no Importer"
			r.Pkg, r.File = cp, cf
			out = append(out, r)
			continue
		}
		r.Pkg, r.File, r.Expr = ip, ifl, expr
		r.ReadPkg, r.ReadFile, r.Read = l.findRead(cp, cf, ctor, 0, map[*ast.FuncDecl]bool{})
		r.CreatePkg, r.CreateFile, r.Create = l.findFuncKey(cp, cf, ctor, "Create", 0, map[*ast.FuncDecl]bool{})
		out = append(out, r)
	}
	return out
}

// findRead is [loader.findImporter] for the resource's Read function: a
// `Read: f` key (or `r.Read = f`) whose value names a function
// declaration. The value test is what tells the resource's Read from the
// `Read: pluginsdk.DefaultTimeout(...)` of its Timeouts block, which is a
// call, not a function reference.
func (l *loader) findRead(p *pkg, f *ast.File, fd *ast.FuncDecl, depth int, seen map[*ast.FuncDecl]bool) (*pkg, *ast.File, ast.Node) {
	return l.findFuncKey(p, f, fd, "Read", depth, seen)
}

// findFuncKey is [loader.findRead] for any function-valued key of the
// resource schema: Read, Create.
func (l *loader) findFuncKey(p *pkg, f *ast.File, fd *ast.FuncDecl, wantKey string, depth int, seen map[*ast.FuncDecl]bool) (*pkg, *ast.File, ast.Node) {
	if fd == nil || fd.Body == nil || seen[fd] {
		return nil, nil, nil
	}
	seen[fd] = true
	var (
		rp    *pkg
		rf    *ast.File
		found *ast.FuncDecl
		calls []*ast.CallExpr
	)
	try := func(key string, value ast.Expr) bool {
		if key != wantKey {
			return false
		}
		switch value.(type) {
		case *ast.Ident, *ast.SelectorExpr:
		default:
			return false
		}
		if vp, vf, vd := l.funcRef(p, f, value); vd != nil {
			rp, rf, found = vp, vf, vd
			return true
		}
		return false
	}
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		switch n := n.(type) {
		case *ast.KeyValueExpr:
			if id, ok := n.Key.(*ast.Ident); ok && try(id.Name, n.Value) {
				return false
			}
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if se, ok := lhs.(*ast.SelectorExpr); ok && i < len(n.Rhs) && try(se.Sel.Name, n.Rhs[i]) {
					return false
				}
			}
		case *ast.CallExpr:
			calls = append(calls, n)
		}
		return true
	})
	if found != nil {
		return rp, rf, found.Body
	}
	if depth >= 2 {
		return nil, nil, nil
	}
	for _, c := range calls {
		cp, cf, cfd := l.funcRef(p, f, c.Fun)
		if cfd == nil || !strings.HasPrefix(cp.path, l.module+"/internal/services/") {
			continue
		}
		if rp, rf, e := l.findFuncKey(cp, cf, cfd, wantKey, depth+1, seen); e != nil {
			return rp, rf, e
		}
	}
	return nil, nil, nil
}

// findImporter looks for `Importer: X` (or `r.Importer = X`) in a resource
// constructor, following calls into same-module helpers a couple of levels
// deep for the constructors that build their resource in a shared function.
func (l *loader) findImporter(p *pkg, f *ast.File, fd *ast.FuncDecl, depth int, seen map[*ast.FuncDecl]bool) (*pkg, *ast.File, ast.Expr) {
	if fd == nil || fd.Body == nil || seen[fd] {
		return nil, nil, nil
	}
	seen[fd] = true
	var found ast.Expr
	var calls []*ast.CallExpr
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		switch n := n.(type) {
		case *ast.KeyValueExpr:
			if id, ok := n.Key.(*ast.Ident); ok && id.Name == "Importer" {
				found = n.Value
				return false
			}
		case *ast.AssignStmt:
			for i, lhs := range n.Lhs {
				if se, ok := lhs.(*ast.SelectorExpr); ok && se.Sel.Name == "Importer" && i < len(n.Rhs) {
					found = n.Rhs[i]
					return false
				}
			}
		case *ast.CallExpr:
			calls = append(calls, n)
		}
		return true
	})
	if found != nil {
		return p, f, found
	}
	if depth >= 2 {
		return nil, nil, nil
	}
	for _, c := range calls {
		cp, cf, cfd := l.funcRef(p, f, c.Fun)
		if cfd == nil || !strings.HasPrefix(cp.path, l.module+"/internal/services/") {
			continue
		}
		if rp, rf, e := l.findImporter(cp, cf, cfd, depth+1, seen); e != nil {
			return rp, rf, e
		}
	}
	return nil, nil, nil
}

func (l *loader) typedRegistrations(p *pkg, f *ast.File, fd *ast.FuncDecl) []registration {
	lit := registeredLit(fd)
	if lit == nil {
		return nil
	}
	var out []registration
	for _, e := range lit.Elts {
		tp, tname := l.typedElement(p, f, e)
		if tp == nil || tname == "" {
			continue
		}
		name := l.resourceTypeName(tp, tname)
		if !strings.HasPrefix(name, "azurerm_") {
			continue
		}
		r := registration{Type: name, Via: "IDValidationFunc"}
		mp, m := l.methodWithEmbedding(tp, tname, "IDValidationFunc", 0)
		if m == nil || m.Body == nil {
			r.Problem = "typed resource has no IDValidationFunc in source"
			out = append(out, r)
			continue
		}
		r.Pkg, r.File, r.Expr = mp, mp.fileOf[m], m.Body
		if rp, rm := l.methodWithEmbedding(tp, tname, "Read", 0); rm != nil && rm.Body != nil {
			r.ReadPkg, r.ReadFile, r.Read = rp, rp.fileOf[rm], rm.Body
		}
		if cp, cm := l.methodWithEmbedding(tp, tname, "Create", 0); cm != nil && cm.Body != nil {
			r.CreatePkg, r.CreateFile, r.Create = cp, cp.fileOf[cm], cm.Body
		}
		out = append(out, r)
	}
	return out
}

// typedElement names the struct type one element of a Resources() slice
// instantiates: `FooResource{}`, `&FooResource{}`, `pkg.FooResource{}`, or
// a constructor call whose first result is that type.
func (l *loader) typedElement(p *pkg, f *ast.File, e ast.Expr) (*pkg, string) {
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

// resourceTypeName reads the string a typed resource's ResourceType()
// returns: a literal, or a package-level constant holding one.
func (l *loader) resourceTypeName(p *pkg, tname string) string {
	mp, m := l.methodWithEmbedding(p, tname, "ResourceType", 0)
	if m == nil || m.Body == nil {
		return ""
	}
	for _, s := range m.Body.List {
		rs, ok := s.(*ast.ReturnStmt)
		if !ok || len(rs.Results) != 1 {
			continue
		}
		if s, ok := stringLit(rs.Results[0]); ok {
			return s
		}
		if id, ok := rs.Results[0].(*ast.Ident); ok {
			if v, ok := mp.consts[id.Name]; ok {
				if s, ok := stringLit(v); ok {
					return s
				}
			}
		}
	}
	return ""
}

// methodWithEmbedding finds a method on a struct type or, failing that, on
// a struct it embeds - a typed resource sometimes inherits IDValidationFunc
// from a shared base.
func (l *loader) methodWithEmbedding(p *pkg, tname, method string, depth int) (*pkg, *ast.FuncDecl) {
	if m := p.method(tname, method); m != nil {
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

// funcRef resolves a call target to its declaration: a same-package
// function or a qualified one in another package of the checkout.
func (l *loader) funcRef(p *pkg, f *ast.File, e ast.Expr) (*pkg, *ast.File, *ast.FuncDecl) {
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

// typeRef resolves a type expression to its package and bare name.
func (l *loader) typeRef(p *pkg, f *ast.File, e ast.Expr) (*pkg, string) {
	switch x := e.(type) {
	case *ast.StarExpr:
		return l.typeRef(p, f, x.X)
	case *ast.IndexExpr:
		// A generic instantiation, commonids.CompositeResourceID[T1] say:
		// the named type is the one before the brackets.
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

func stringLit(e ast.Expr) (string, bool) {
	bl, ok := e.(*ast.BasicLit)
	if !ok {
		return "", false
	}
	s, err := strconv.Unquote(bl.Value)
	if err != nil {
		return "", false
	}
	return s, true
}
