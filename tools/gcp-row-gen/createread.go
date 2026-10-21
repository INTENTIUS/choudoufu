// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	gotoken "go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// This file reads a hand-written resource's Create at code level (#2145
// item 1). A Magic Modules type states its id as the template it passes to
// tpgresource.ReplaceVars, which extract.go reads with one regex. A
// hand-written type builds its id in code: fmt.Sprintf over values bound
// from d.Get and tpgresource.GetProject, a helper of its package, a struct
// method, a DCL resource's ID(). Here the provider package is parsed with
// go/parser (never type-checked, built or run) and the argument of each
// d.SetId in the Create is evaluated symbolically into the same kind of
// template, with what the provider does to each value on the way: keep its
// last path segment, trim a literal prefix, or read it raw with no provider
// default. A value the evaluator does not model is never guessed: the
// template is left unread and the reason says what stopped it.
//
// The importer is read the same way: whether it is the SDK's passthrough,
// absent, or code of its own, and for the last, its ParseImportId regexes
// and the id it sets.

// piece is one part of an evaluated id: a literal, or one argument.
type piece struct {
	Lit string
	// Attr is the argument the value comes from.
	Attr string
	// Via is how it is read: "get" (d.Get, as written, no default),
	// "project", "region" or "zone" (tpgresource.GetProject and friends,
	// the provider's value when the argument is unset), "location"
	// (tpgresource.GetLocation), or "vars" (a ReplaceVars template).
	Via string
	// Short is set when the provider keeps only the value's last path
	// segment (GetResourceNameFromSelfLink, ReplaceVarsForId, a DCL
	// SelfLinkToName).
	Short bool
	// Trim is a literal prefix the provider trims off the value.
	Trim string
}

// rval is the value of one expression.
type rval struct {
	kind   rkind
	pieces []piece
	// typ and fields are a struct value's type name and field
	// expressions; entries is a map literal's.
	typ     string
	fields  map[string]thunk
	entries map[string]thunk
	fn      *funcVal
	// why says what stopped the evaluation; minted is set when it stopped
	// at a value read out of what the API returned.
	why    string
	minted bool
}

type rkind int

const (
	kUnknown rkind = iota
	kPieces
	kStruct
	kMap
	kFunc
)

type thunk struct {
	e   ast.Expr
	env *renv
	// v is a value already evaluated, used when e is nil.
	v *rval
}

type funcVal struct {
	decl *ast.FuncDecl
	lit  *ast.FuncLit
	pkg  *pkgIndex
	// env is a closure's enclosing scope.
	env *renv
}

func (f *funcVal) body() *ast.BlockStmt {
	if f.decl != nil {
		return f.decl.Body
	}
	return f.lit.Body
}

func (f *funcVal) name() string {
	if f.decl != nil {
		return f.decl.Name.Name
	}
	return "a function literal"
}

func (f *funcVal) params() *ast.FieldList {
	if f.decl != nil {
		return f.decl.Type.Params
	}
	return f.lit.Type.Params
}

// renv is one function's scope.
type renv struct {
	pkg    *pkgIndex
	body   *ast.BlockStmt
	binds  map[string]rval
	parent *renv
	depth  int
	// resolving guards against a variable defined in terms of itself.
	resolving map[string]bool
	// fnName names the function, for a reason.
	fnName string
}

func unknown(format string, args ...any) rval {
	return rval{kind: kUnknown, why: fmt.Sprintf(format, args...)}
}

func mintedVal(format string, args ...any) rval {
	return rval{kind: kUnknown, why: fmt.Sprintf(format, args...), minted: true}
}

func lit(s string) rval { return rval{kind: kPieces, pieces: []piece{{Lit: s}}} }

// pkgIndex is one provider package, parsed.
type pkgIndex struct {
	dir     string
	repo    string
	fset    *gotoken.FileSet
	funcs   map[string]*ast.FuncDecl
	methods map[string]*ast.FuncDecl
	consts  map[string]ast.Expr
	files   []*ast.File
	// imports maps a file's import name to the provider package path it
	// names, for the provider's own packages only.
	imports map[*ast.File]map[string]string
	loader  *pkgLoader
}

// pkgLoader parses each package directory once.
type pkgLoader struct {
	repo   string
	module string
	pkgs   map[string]*pkgIndex
}

func newPkgLoader(repo string) *pkgLoader {
	l := &pkgLoader{repo: repo, pkgs: map[string]*pkgIndex{}}
	if data, err := os.ReadFile(filepath.Join(repo, "go.mod")); err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			if m, ok := strings.CutPrefix(strings.TrimSpace(line), "module "); ok {
				l.module = strings.TrimSpace(m)
				break
			}
		}
	}
	return l
}

func (l *pkgLoader) load(dir string) (*pkgIndex, error) {
	if p, ok := l.pkgs[dir]; ok {
		return p, nil
	}
	p := &pkgIndex{
		dir: dir, repo: l.repo, fset: gotoken.NewFileSet(),
		funcs: map[string]*ast.FuncDecl{}, methods: map[string]*ast.FuncDecl{}, consts: map[string]ast.Expr{},
		imports: map[*ast.File]map[string]string{}, loader: l,
	}
	l.pkgs[dir] = p
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		n := e.Name()
		if e.IsDir() || !strings.HasSuffix(n, ".go") || strings.HasSuffix(n, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(p.fset, filepath.Join(dir, n), nil, parser.SkipObjectResolution)
		if err != nil {
			return nil, err
		}
		p.files = append(p.files, f)
		imps := map[string]string{}
		for _, im := range f.Imports {
			path, _ := strconv.Unquote(im.Path.Value)
			name := path[strings.LastIndex(path, "/")+1:]
			if im.Name != nil {
				name = im.Name.Name
			}
			imps[name] = path
		}
		p.imports[f] = imps
		for _, d := range f.Decls {
			switch d := d.(type) {
			case *ast.FuncDecl:
				if d.Recv == nil {
					p.funcs[d.Name.Name] = d
					continue
				}
				if len(d.Recv.List) == 1 {
					p.methods[recvType(d.Recv.List[0].Type)+"."+d.Name.Name] = d
				}
			case *ast.GenDecl:
				if d.Tok != gotoken.CONST && d.Tok != gotoken.VAR {
					continue
				}
				for _, s := range d.Specs {
					vs, ok := s.(*ast.ValueSpec)
					if !ok || len(vs.Names) != len(vs.Values) {
						continue
					}
					for i, n := range vs.Names {
						p.consts[n.Name] = vs.Values[i]
					}
				}
			}
		}
	}
	return p, nil
}

// pkgFor is the provider package an import name in file f refers to, or
// nil for anything outside the provider's own module.
func (p *pkgIndex) pkgFor(f *ast.File, name string) *pkgIndex {
	path, ok := p.imports[f][name]
	if !ok || p.loader.module == "" || !strings.HasPrefix(path, p.loader.module+"/") {
		return nil
	}
	q, err := p.loader.load(filepath.Join(p.loader.repo, filepath.FromSlash(strings.TrimPrefix(path, p.loader.module+"/"))))
	if err != nil {
		return nil
	}
	return q
}

// fileOf is the file a node is in.
func (p *pkgIndex) fileOf(n ast.Node) *ast.File {
	for _, f := range p.files {
		if f.Pos() <= n.Pos() && n.End() <= f.End() {
			return f
		}
	}
	return nil
}

// at is "path:line" of a node, relative to the provider repository.
func (p *pkgIndex) at(n ast.Node) string {
	pos := p.fset.Position(n.Pos())
	rel, err := filepath.Rel(p.repo, pos.Filename)
	if err != nil {
		rel = pos.Filename
	}
	return fmt.Sprintf("%s:%d", filepath.ToSlash(rel), pos.Line)
}

// text is a node's source on one line, cut short: it appears in reasons.
func (p *pkgIndex) text(n ast.Node) string {
	s := strings.Join(strings.Fields(p.source(n)), " ")
	if len(s) > 100 {
		s = s[:97] + "..."
	}
	return s
}

func (p *pkgIndex) source(n ast.Node) string {
	var b bytes.Buffer
	_ = printer.Fprint(&b, p.fset, n)
	return b.String()
}

func recvType(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return recvType(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.IndexExpr:
		return recvType(e.X)
	}
	return ""
}

func typeName(e ast.Expr) string {
	switch e := e.(type) {
	case *ast.StarExpr:
		return typeName(e.X)
	case *ast.Ident:
		return e.Name
	case *ast.SelectorExpr:
		return e.Sel.Name
	}
	return ""
}

const maxDepth = 60

func (env *renv) child(body *ast.BlockStmt, binds map[string]rval, parent *renv) *renv {
	return &renv{pkg: env.pkg, body: body, binds: binds, parent: parent, depth: env.depth + 1, resolving: map[string]bool{}}
}

// eval is the value of e in env.
func (env *renv) eval(e ast.Expr) rval {
	if env.depth > maxDepth {
		return unknown("the value is built deeper than this reader follows")
	}
	switch e := e.(type) {
	case *ast.BasicLit:
		if e.Kind == gotoken.STRING {
			s, err := strconv.Unquote(e.Value)
			if err != nil {
				return unknown("unreadable literal %s", e.Value)
			}
			return lit(s)
		}
		return unknown("a %s literal", e.Kind)
	case *ast.ParenExpr:
		return env.eval(e.X)
	case *ast.StarExpr:
		return env.eval(e.X)
	case *ast.UnaryExpr:
		if e.Op == gotoken.AND {
			return env.eval(e.X)
		}
	case *ast.TypeAssertExpr:
		v := env.eval(e.X)
		if v.kind == kStruct && e.Type != nil {
			if n := typeName(e.Type); n != "" {
				v.typ = n
			}
		}
		return v
	case *ast.Ident:
		return env.ident(e.Name)
	case *ast.CompositeLit:
		return env.composite(e)
	case *ast.FuncLit:
		return rval{kind: kFunc, fn: &funcVal{lit: e, pkg: env.pkg, env: env}}
	case *ast.BinaryExpr:
		if e.Op == gotoken.ADD {
			a, b := env.eval(e.X), env.eval(e.Y)
			if a.kind == kUnknown {
				return a
			}
			if b.kind == kUnknown {
				return b
			}
			if a.kind == kPieces && b.kind == kPieces {
				return rval{kind: kPieces, pieces: append(append([]piece(nil), a.pieces...), b.pieces...)}
			}
		}
	case *ast.SelectorExpr:
		return env.selector(e)
	case *ast.IndexExpr:
		m := env.eval(e.X)
		if m.kind == kUnknown {
			return m
		}
		k := env.eval(e.Index)
		key, ok := literalOf(k)
		if m.kind == kMap && ok {
			if t, found := m.entries[key]; found {
				return t.force()
			}
			return unknown("the map has no %q key", key)
		}
	case *ast.CallExpr:
		return env.call(e, 0)
	}
	return unknown("%s is an expression this reader does not evaluate", env.pkg.text(e))
}

func (t thunk) force() rval {
	if t.e == nil {
		if t.v != nil {
			return *t.v
		}
		return unknown("no value")
	}
	return t.env.eval(t.e)
}

func literalOf(v rval) (string, bool) {
	if v.kind != kPieces {
		return "", false
	}
	var b strings.Builder
	for _, p := range v.pieces {
		if p.Attr != "" {
			return "", false
		}
		b.WriteString(p.Lit)
	}
	return b.String(), true
}

func (env *renv) composite(e *ast.CompositeLit) rval {
	if _, isMap := e.Type.(*ast.MapType); isMap {
		out := rval{kind: kMap, entries: map[string]thunk{}}
		for _, el := range e.Elts {
			kv, ok := el.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			if k, ok := literalOf(env.eval(kv.Key)); ok {
				out.entries[k] = thunk{e: kv.Value, env: env}
			}
		}
		return out
	}
	out := rval{kind: kStruct, typ: typeName(e.Type), fields: map[string]thunk{}}
	for _, el := range e.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		if k, ok := kv.Key.(*ast.Ident); ok {
			out.fields[k.Name] = thunk{e: kv.Value, env: env}
		}
	}
	return out
}

// assignment is one place a local variable gets a value.
type assignment struct {
	rhs ast.Expr
	// idx is the result index of a multi-value call.
	idx int
	// none is set for a declaration with no value, or a range variable
	// (loop).
	none, loop bool
}

// assignments are every place in body (closures included) that name is
// given a value, and every `name.Field = x`.
func assignments(body *ast.BlockStmt, name string) (vals []assignment, fields map[string][]ast.Expr) {
	fields = map[string][]ast.Expr{}
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.AssignStmt:
			for i, l := range n.Lhs {
				switch l := l.(type) {
				case *ast.Ident:
					if l.Name != name {
						continue
					}
					switch {
					case len(n.Rhs) == len(n.Lhs):
						vals = append(vals, assignment{rhs: n.Rhs[i]})
					case len(n.Rhs) == 1:
						vals = append(vals, assignment{rhs: n.Rhs[0], idx: i})
					default:
						vals = append(vals, assignment{none: true})
					}
				case *ast.SelectorExpr:
					if x, ok := l.X.(*ast.Ident); ok && x.Name == name && len(n.Rhs) == len(n.Lhs) {
						fields[l.Sel.Name] = append(fields[l.Sel.Name], n.Rhs[i])
					}
				}
			}
		case *ast.ValueSpec:
			for i, id := range n.Names {
				if id.Name != name {
					continue
				}
				switch {
				case len(n.Values) == len(n.Names):
					vals = append(vals, assignment{rhs: n.Values[i]})
				case len(n.Values) == 1:
					vals = append(vals, assignment{rhs: n.Values[0], idx: i})
				default:
					vals = append(vals, assignment{none: true})
				}
			}
		case *ast.RangeStmt:
			for _, x := range []ast.Expr{n.Key, n.Value} {
				if id, ok := x.(*ast.Ident); ok && id.Name == name {
					vals = append(vals, assignment{none: true, loop: true})
				}
			}
		}
		return true
	})
	return vals, fields
}

func (env *renv) ident(name string) rval {
	for s := env; s != nil; s = s.parent {
		if v, ok := s.binds[name]; ok {
			return v
		}
		if s.body == nil {
			continue
		}
		vals, fields := assignments(s.body, name)
		if len(vals) == 0 && len(fields) == 0 {
			continue
		}
		if s.resolving[name] {
			return unknown("%s is defined in terms of itself", name)
		}
		s.resolving[name] = true
		v := s.resolveLocal(name, vals, fields)
		delete(s.resolving, name)
		return v
	}
	if name == "nil" {
		return unknown("nil")
	}
	if c, ok := env.pkg.consts[name]; ok {
		root := &renv{pkg: env.pkg, depth: env.depth + 1, resolving: map[string]bool{}}
		return root.eval(c)
	}
	if f, ok := env.pkg.funcs[name]; ok {
		return rval{kind: kFunc, fn: &funcVal{decl: f, pkg: env.pkg}}
	}
	return unknown("%s is bound nowhere this reader looks", name)
}

func (env *renv) resolveLocal(name string, vals []assignment, fields map[string][]ast.Expr) rval {
	// `x = strings.TrimPrefix(x, "p")`, however guarded, is a trim of
	// what x held before: TrimPrefix leaves a value without the prefix
	// alone.
	var trims []string
	var rest []assignment
	for _, a := range vals {
		if p, ok := env.selfTrim(name, a); ok {
			trims = append(trims, p)
			continue
		}
		rest = append(rest, a)
	}
	if len(trims) > 0 {
		if len(trims) != 1 || len(rest) != 1 {
			return unknown("%s is trimmed and assigned %d times", name, len(vals))
		}
		return trimPieces(env.resolveLocal(name, rest, nil), trims[0])
	}
	var got []rval
	for _, a := range vals {
		if a.loop {
			got = append(got, unknown("%s, a loop variable", name))
			continue
		}
		if a.none {
			got = append(got, unknown("%s, declared with no value and filled in elsewhere", name))
			continue
		}
		if call, ok := a.rhs.(*ast.CallExpr); ok {
			got = append(got, env.call(call, a.idx))
			continue
		}
		if a.idx != 0 {
			got = append(got, unknown("%s is the %d-th value of %s", name, a.idx, env.pkg.text(a.rhs)))
			continue
		}
		got = append(got, env.eval(a.rhs))
	}
	if len(got) == 0 {
		return unknown("%s has fields set but no value", name)
	}
	v := got[0]
	for _, o := range got[1:] {
		if !sameValue(v, o) {
			return unknown("%s is assigned %d times, to different values", name, len(got))
		}
	}
	if v.kind == kStruct && len(fields) > 0 {
		nf := map[string]thunk{}
		for k, t := range v.fields {
			nf[k] = t
		}
		for f, rhs := range fields {
			if len(rhs) != 1 {
				u := unknown("%s.%s is assigned %d times", name, f, len(rhs))
				nf[f] = thunk{v: &u}
				continue
			}
			nf[f] = thunk{e: rhs[0], env: env}
		}
		v.fields = nf
	}
	return v
}

// selfTrim reports whether a is `name = strings.TrimPrefix(name, "p")`.
func (env *renv) selfTrim(name string, a assignment) (string, bool) {
	call, ok := a.rhs.(*ast.CallExpr)
	if !ok || a.idx != 0 || len(call.Args) != 2 || env.pkg.text(call.Fun) != "strings.TrimPrefix" {
		return "", false
	}
	if x, ok := call.Args[0].(*ast.Ident); !ok || x.Name != name {
		return "", false
	}
	return literalOf(env.eval(call.Args[1]))
}

func sameValue(a, b rval) bool {
	if a.kind != b.kind {
		return false
	}
	switch a.kind {
	case kPieces:
		return renderPieces(a.pieces) == renderPieces(b.pieces) && fmt.Sprint(a.pieces) == fmt.Sprint(b.pieces)
	case kFunc:
		return a.fn.decl == b.fn.decl && a.fn.lit == b.fn.lit
	case kUnknown:
		return a.why == b.why
	}
	return false
}

// providerGetters are the tpgresource functions that read a scope
// argument with the provider's value as its fallback.
var providerGetters = map[string]string{
	"GetProject": "project", "GetRegion": "region", "GetZone": "zone", "GetLocation": "location",
}

// selector is x.F that is not a call.
func (env *renv) selector(e *ast.SelectorExpr) rval {
	if x, ok := e.X.(*ast.Ident); ok && env.imported(e, x.Name) && !env.isLocal(x.Name) {
		return unknown("%s is another package's value", env.pkg.text(e))
	}
	x := env.eval(e.X)
	switch x.kind {
	case kStruct:
		t, ok := x.fields[e.Sel.Name]
		if !ok {
			return unknown("%s leaves %s unset", x.typ, e.Sel.Name)
		}
		return t.force()
	case kUnknown:
		if x.minted {
			return mintedVal("%s, read from what the API returned", env.pkg.text(e))
		}
		return x
	}
	return unknown("%s selects a field of something that is not a struct", env.pkg.text(e))
}

// imported reports whether name is an import of the file n is in.
func (env *renv) imported(n ast.Node, name string) bool {
	f := env.pkg.fileOf(n)
	if f == nil {
		return false
	}
	_, ok := env.pkg.imports[f][name]
	return ok
}

// isLocal reports whether name is a variable of the function, which
// shadows an import of the same name.
func (env *renv) isLocal(name string) bool {
	for s := env; s != nil; s = s.parent {
		if _, ok := s.binds[name]; ok {
			return true
		}
		if s.body != nil {
			if vals, _ := assignments(s.body, name); len(vals) > 0 {
				return true
			}
		}
	}
	return false
}

// apiCall reports whether a call is one this reader treats as asking the
// API: a generated client's .Do(), transport_tpg.SendRequest, or a DCL
// client's Apply.
func apiCall(e *ast.CallExpr) bool {
	sel, ok := e.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	switch {
	case sel.Sel.Name == "Do" && len(e.Args) == 0:
		return true
	case sel.Sel.Name == "SendRequest":
		return true
	case strings.HasPrefix(sel.Sel.Name, "Apply"):
		return true
	}
	return false
}

// call is the idx-th result of a call.
func (env *renv) call(e *ast.CallExpr, idx int) rval {
	if apiCall(e) {
		return mintedVal("%s, what the API returned", env.pkg.text(e))
	}
	switch fun := e.Fun.(type) {
	case *ast.SelectorExpr:
		if x, ok := fun.X.(*ast.Ident); ok && env.imported(e, x.Name) && !env.isLocal(x.Name) {
			return env.qualified(env.pkg.fileOf(e), x.Name, fun.Sel.Name, e, idx)
		}
		// d.Get("name")
		if x, ok := fun.X.(*ast.Ident); ok && (x.Name == "d" || x.Name == "diff") {
			switch fun.Sel.Name {
			case "Get":
				if idx != 0 || len(e.Args) != 1 {
					return unknown("%s", env.pkg.text(e))
				}
				if name, ok := literalOf(env.eval(e.Args[0])); ok {
					return rval{kind: kPieces, pieces: []piece{{Attr: name, Via: "get"}}}
				}
				return unknown("%s reads an argument named by a value", env.pkg.text(e))
			case "Id":
				return unknown("%s, the id itself", env.pkg.text(e))
			}
			return unknown("%s", env.pkg.text(e))
		}
		recv := env.eval(fun.X)
		if recv.kind == kUnknown {
			if recv.minted {
				return mintedVal("%s, read from what the API returned", env.pkg.text(e))
			}
			return recv
		}
		if recv.kind != kStruct {
			return unknown("%s calls a method on something that is not a struct", env.pkg.text(e))
		}
		m, ok := env.pkg.methods[recv.typ+"."+fun.Sel.Name]
		if !ok {
			return unknown("%s calls %s.%s, which this package does not define", env.pkg.text(e), recv.typ, fun.Sel.Name)
		}
		binds := map[string]rval{}
		if names := m.Recv.List[0].Names; len(names) == 1 {
			binds[names[0].Name] = recv
		}
		return env.invoke(&funcVal{decl: m, pkg: env.pkg}, e.Args, binds, idx)
	case *ast.Ident:
		f := env.ident(fun.Name)
		if f.kind != kFunc {
			if f.kind == kUnknown {
				return f
			}
			return unknown("%s calls something that is not a function", env.pkg.text(e))
		}
		return env.invoke(f.fn, e.Args, nil, idx)
	case *ast.FuncLit:
		return env.invoke(&funcVal{lit: fun, pkg: env.pkg, env: env}, e.Args, nil, idx)
	}
	return unknown("%s is a call this reader does not follow", env.pkg.text(e))
}

// qualified is pkg.Name(args): a provider helper this reader models, or a
// function of another provider package, followed.
func (env *renv) qualified(f *ast.File, pkgName, name string, e *ast.CallExpr, idx int) rval {
	full := pkgName + "." + name
	arg := func(i int) rval {
		if i >= len(e.Args) {
			return unknown("%s has too few arguments", full)
		}
		return env.eval(e.Args[i])
	}
	shorten := func(v rval) rval {
		if v.kind != kPieces {
			return v
		}
		if s, ok := literalOf(v); ok {
			return lit(lastSegment(s))
		}
		if len(v.pieces) != 1 || v.pieces[0].Trim != "" {
			return unknown("%s over a value that is not one argument", full)
		}
		p := v.pieces[0]
		p.Short = true
		return rval{kind: kPieces, pieces: []piece{p}}
	}
	switch {
	case pkgName == "tpgresource" && providerGetters[name] != "":
		if idx != 0 {
			return unknown("the error of %s", full)
		}
		via := providerGetters[name]
		return rval{kind: kPieces, pieces: []piece{{Attr: via, Via: via}}}
	case pkgName == "tpgresource" && (name == "ReplaceVars" || name == "ReplaceVarsForId"):
		if idx != 0 {
			return unknown("the error of %s", full)
		}
		tmpl, ok := literalOf(arg(2))
		if !ok {
			return unknown("%s over a template that is not a literal", full)
		}
		return varsTemplate(tmpl, name == "ReplaceVarsForId")
	case full == "tpgresource.GetResourceNameFromSelfLink", name == "SelfLinkToName", name == "SelfLinkToNameExpander":
		return shorten(arg(0))
	case pkgName == "dcl" && (name == "String" || name == "ValueOrEmptyString" || name == "Copy"):
		return arg(0)
	case pkgName == "dcl" && name == "Nprintf":
		tmpl, ok := literalOf(arg(0))
		if !ok {
			return unknown("%s over a template that is not a literal", full)
		}
		params := arg(1)
		if params.kind != kMap {
			return unknown("%s over parameters that are not a map literal", full)
		}
		out := rval{kind: kPieces}
		for _, tok := range tokenize(tmpl) {
			if tok.name == "" {
				out.pieces = append(out.pieces, piece{Lit: tok.literal})
				continue
			}
			t, ok := params.entries[tok.name]
			if !ok {
				return unknown("%s names {{%s}}, which its parameters do not", full, tok.name)
			}
			v := t.force()
			if v.kind != kPieces {
				return v
			}
			out.pieces = append(out.pieces, v.pieces...)
		}
		return out
	case full == "fmt.Sprintf":
		format, ok := literalOf(arg(0))
		if !ok {
			return unknown("fmt.Sprintf with a format that is not a literal")
		}
		return sprintf(format, e.Args[1:], env)
	case full == "strings.TrimPrefix":
		prefix, ok := literalOf(arg(1))
		if !ok {
			return unknown("strings.TrimPrefix by a prefix that is not a literal")
		}
		return trimPieces(arg(0), prefix)
	case pkgName == "url" && (name == "QueryEscape" || name == "PathEscape"):
		return unknown("%s escapes the value, which a row cannot reproduce", env.pkg.text(e))
	}
	if q := env.pkg.pkgFor(f, pkgName); q != nil {
		if fd, ok := q.funcs[name]; ok {
			return env.invoke(&funcVal{decl: fd, pkg: q}, e.Args, nil, idx)
		}
	}
	return unknown("%s, which this reader does not model", env.pkg.text(e))
}

func lastSegment(s string) string {
	return s[strings.LastIndex(s, "/")+1:]
}

func trimPieces(v rval, prefix string) rval {
	if v.kind != kPieces {
		return v
	}
	if len(v.pieces) != 1 || v.pieces[0].Attr == "" || v.pieces[0].Trim != "" || v.pieces[0].Short {
		return unknown("a prefix trimmed off something that is not one argument")
	}
	p := v.pieces[0]
	p.Trim = prefix
	return rval{kind: kPieces, pieces: []piece{p}}
}

// varsTemplate is what tpgresource.ReplaceVars builds from a template:
// project, region and zone through the provider getters, anything else
// as written (BuildReplacementFunc at v8.6.0).
func varsTemplate(tmpl string, short bool) rval {
	out := rval{kind: kPieces}
	for _, tok := range tokenize(tmpl) {
		switch {
		case tok.name == "":
			out.pieces = append(out.pieces, piece{Lit: tok.literal})
		case tok.escaped:
			return unknown("the template path-escapes {{%%%s}}, which a row cannot reproduce", tok.name)
		default:
			via := "vars"
			if _, cloud := cloudPlaceholders[tok.name]; cloud {
				via = tok.name
			}
			out.pieces = append(out.pieces, piece{Attr: tok.name, Via: via, Short: short})
		}
	}
	return out
}

func sprintf(format string, args []ast.Expr, env *renv) rval {
	out := rval{kind: kPieces}
	n := 0
	for i := 0; i < len(format); i++ {
		c := format[i]
		if c != '%' {
			out.pieces = append(out.pieces, piece{Lit: string(c)})
			continue
		}
		if i+1 >= len(format) {
			return unknown("fmt.Sprintf format %q ends in %%", format)
		}
		i++
		switch format[i] {
		case '%':
			out.pieces = append(out.pieces, piece{Lit: "%"})
		case 's', 'v':
			if n >= len(args) {
				return unknown("fmt.Sprintf format %q has more verbs than arguments", format)
			}
			v := env.eval(args[n])
			n++
			if v.kind != kPieces {
				if v.kind == kUnknown {
					return v
				}
				return unknown("fmt.Sprintf formats %s, which is not a string", env.pkg.text(args[n-1]))
			}
			out.pieces = append(out.pieces, v.pieces...)
		default:
			return unknown("fmt.Sprintf format %q uses %%%c", format, format[i])
		}
	}
	if n != len(args) {
		return unknown("fmt.Sprintf format %q has fewer verbs than arguments", format)
	}
	return merged(out)
}

// merged joins adjacent literals.
func merged(v rval) rval {
	if v.kind != kPieces {
		return v
	}
	var out []piece
	for _, p := range v.pieces {
		if p.Attr == "" && len(out) > 0 && out[len(out)-1].Attr == "" {
			out[len(out)-1].Lit += p.Lit
			continue
		}
		out = append(out, p)
	}
	return rval{kind: kPieces, pieces: out}
}

// invoke is the idx-th result of calling fn with args.
func (env *renv) invoke(fn *funcVal, args []ast.Expr, binds map[string]rval, idx int) rval {
	if binds == nil {
		binds = map[string]rval{}
	}
	i := 0
	for _, field := range fn.params().List {
		names := field.Names
		if len(names) == 0 {
			i++
			continue
		}
		for _, n := range names {
			if i < len(args) {
				binds[n.Name] = env.eval(args[i])
			}
			i++
		}
	}
	parent := fn.env
	callee := &renv{pkg: fn.pkg, body: fn.body(), binds: binds, parent: parent, depth: env.depth + 1, resolving: map[string]bool{}, fnName: fn.name()}
	return callee.returned(idx)
}

// returned is the idx-th result the function returns. Of its return
// statements (closures excluded), those returning nil or "" there are
// error paths; exactly one other may remain. The one other shape read is
// a prefix trim:
//
//	if strings.HasPrefix(v, "folders/") {
//		return v[8:]
//	}
//	return v
func (env *renv) returned(idx int) rval {
	var rets []*ast.ReturnStmt
	ast.Inspect(env.body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			rets = append(rets, n)
		}
		return true
	})
	var cands []ast.Expr
	for _, r := range rets {
		if idx >= len(r.Results) {
			if len(r.Results) == 1 && idx > 0 {
				return unknown("a call whose results this reader cannot split")
			}
			continue
		}
		res := r.Results[idx]
		if id, ok := res.(*ast.Ident); ok && id.Name == "nil" {
			continue
		}
		if bl, ok := res.(*ast.BasicLit); ok && bl.Value == `""` {
			continue
		}
		cands = append(cands, res)
	}
	if len(cands) == 1 {
		return env.eval(cands[0])
	}
	if len(cands) == 2 && idx == 0 {
		if v, ok := env.prefixTrim(cands[0], cands[1]); ok {
			return v
		}
	}
	if len(cands) == 0 {
		return unknown("%s, which returns no value there", env.fnName)
	}
	return unknown("%s, which returns %d different values", env.fnName, len(cands))
}

func (env *renv) prefixTrim(first, second ast.Expr) (rval, bool) {
	sl, ok := first.(*ast.SliceExpr)
	if !ok || sl.High != nil || sl.Low == nil {
		return rval{}, false
	}
	x, ok := sl.X.(*ast.Ident)
	y, ok2 := second.(*ast.Ident)
	if !ok || !ok2 || x.Name != y.Name {
		return rval{}, false
	}
	low, ok := sl.Low.(*ast.BasicLit)
	if !ok || low.Kind != gotoken.INT {
		return rval{}, false
	}
	n, err := strconv.Atoi(low.Value)
	if err != nil {
		return rval{}, false
	}
	// The if guarding the first return.
	var prefix string
	found := false
	ast.Inspect(env.body, func(node ast.Node) bool {
		ifs, ok := node.(*ast.IfStmt)
		if !ok || found {
			return !found
		}
		call, ok := ifs.Cond.(*ast.CallExpr)
		if !ok || len(ifs.Body.List) != 1 || len(call.Args) != 2 {
			return true
		}
		ret, ok := ifs.Body.List[0].(*ast.ReturnStmt)
		if !ok || len(ret.Results) == 0 || ret.Results[0] != first {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || env.pkg.text(sel) != "strings.HasPrefix" {
			return true
		}
		if a, ok := call.Args[0].(*ast.Ident); !ok || a.Name != x.Name {
			return true
		}
		if p, ok := literalOf(env.eval(call.Args[1])); ok && len(p) == n {
			prefix, found = p, true
		}
		return true
	})
	if !found {
		return rval{}, false
	}
	return trimPieces(env.eval(second), prefix), true
}

// renderPieces is the template the pieces spell.
func renderPieces(ps []piece) string {
	var b strings.Builder
	for _, p := range ps {
		if p.Attr == "" {
			b.WriteString(p.Lit)
			continue
		}
		b.WriteString("{{" + p.Attr + "}}")
	}
	return b.String()
}

// idSite is one d.SetId call.
type idSite struct {
	at    string
	text  string
	value rval
}

// setIDs are the non-empty d.SetId calls in fn's body, with closures
// read in their own scope. When the body sets none, the functions of the
// package it calls with d are read the same way, two levels deep.
func setIDs(fn *funcVal, binds map[string]rval, depth int) []idSite {
	root := &renv{pkg: fn.pkg, body: fn.body(), binds: binds, parent: fn.env, resolving: map[string]bool{}}
	var out []idSite
	var walk func(env *renv, n ast.Node)
	walk = func(env *renv, n ast.Node) {
		ast.Inspect(n, func(node ast.Node) bool {
			switch node := node.(type) {
			case *ast.FuncLit:
				if node.Body != nil {
					walk(env.child(node.Body, map[string]rval{}, env), node.Body)
				}
				return false
			case *ast.CallExpr:
				sel, ok := node.Fun.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "SetId" || len(node.Args) != 1 {
					return true
				}
				if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "d" {
					return true
				}
				if bl, ok := node.Args[0].(*ast.BasicLit); ok && bl.Value == `""` {
					return true
				}
				out = append(out, idSite{at: fn.pkg.at(node), text: fn.pkg.text(node), value: merged(env.eval(node.Args[0]))})
			}
			return true
		})
	}
	walk(root, fn.body())
	if len(out) > 0 || depth >= 2 {
		return out
	}
	// Delegation: a call of a package function that passes d along.
	ast.Inspect(fn.body(), func(node ast.Node) bool {
		if _, ok := node.(*ast.FuncLit); ok {
			return false
		}
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok {
			return true
		}
		passesD := false
		for _, a := range call.Args {
			if x, ok := a.(*ast.Ident); ok && x.Name == "d" {
				passesD = true
			}
		}
		callee := root.ident(id.Name)
		if !passesD || callee.kind != kFunc {
			return true
		}
		b := map[string]rval{}
		i := 0
		for _, field := range callee.fn.params().List {
			for _, n := range field.Names {
				if i < len(call.Args) {
					b[n.Name] = root.eval(call.Args[i])
				}
				i++
			}
		}
		out = append(out, setIDs(callee.fn, b, depth+1)...)
		return true
	})
	return out
}

// HandwrittenID is what the reader finds for one type.
type HandwrittenID struct {
	CreateID      string
	CreateIDAt    []string
	CreateUnread  string
	CreateMinted  bool
	CreateRaw     []string
	CreateShort   []string
	CreateTrim    map[string]string
	CreateLocated []string

	Importer        string
	ImporterAt      string
	ImporterHash    string
	ImporterFormats []string
	ImporterID      string
	ImporterUnread  string
}

// readHandwritten reads typeName's Create and importer from the provider
// package in dir, starting at the registry.Schema literal that registers
// it.
func readHandwritten(loader *pkgLoader, dir, typeName string) (HandwrittenID, error) {
	p, err := loader.load(dir)
	if err != nil {
		return HandwrittenID{}, err
	}
	var schemaExpr ast.Expr
	for _, f := range p.files {
		ast.Inspect(f, func(n ast.Node) bool {
			cl, ok := n.(*ast.CompositeLit)
			if !ok || p.text(cl.Type) != "registry.Schema" {
				return true
			}
			var name string
			var sch ast.Expr
			for _, el := range cl.Elts {
				kv, ok := el.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				switch p.text(kv.Key) {
				case "Name":
					if bl, ok := kv.Value.(*ast.BasicLit); ok {
						name, _ = strconv.Unquote(bl.Value)
					}
				case "Schema":
					sch = kv.Value
				}
			}
			if name == typeName {
				schemaExpr = sch
			}
			return true
		})
	}
	var out HandwrittenID
	if schemaExpr == nil {
		out.CreateUnread = "no registry.Schema literal names this type"
		return out, nil
	}
	root := &renv{pkg: p, resolving: map[string]bool{}}
	res := root.eval(schemaExpr)
	if res.kind != kStruct || res.typ != "Resource" {
		out.CreateUnread = fmt.Sprintf("its registered schema %s is not read as a schema.Resource literal: %s", p.text(schemaExpr), res.why)
		return out, nil
	}
	readCreate(&out, res)
	readImporter(&out, res)
	return out, nil
}

func readCreate(out *HandwrittenID, res rval) {
	t, ok := res.fields["Create"]
	if !ok {
		t, ok = res.fields["CreateContext"]
	}
	if !ok {
		out.CreateUnread = "the schema.Resource sets no Create"
		return
	}
	fv := t.force()
	if fv.kind != kFunc {
		out.CreateUnread = fmt.Sprintf("its Create is not read as a function: %s", fv.why)
		return
	}
	sites := setIDs(fv.fn, nil, 0)
	if len(sites) == 0 {
		out.CreateIDAt = []string{fv.fn.pkg.at(fv.fn.body())}
		out.CreateUnread = "its Create sets no id this reader finds"
		return
	}
	seen := map[string]bool{}
	var uniq []idSite
	for _, s := range sites {
		if !seen[s.at] {
			seen[s.at] = true
			uniq = append(uniq, s)
		}
	}
	var first rval
	for i, s := range uniq {
		if s.value.kind != kPieces {
			why := s.value.why
			if s.value.kind != kUnknown {
				why = "a value that is not a string"
			}
			out.CreateIDAt = []string{s.at}
			out.CreateUnread = fmt.Sprintf("%s sets it from %s", s.text, why)
			out.CreateMinted = s.value.minted
			return
		}
		out.CreateIDAt = append(out.CreateIDAt, s.at)
		if i == 0 {
			first = s.value
			continue
		}
		if fmt.Sprint(first.pieces) != fmt.Sprint(s.value.pieces) {
			out.CreateIDAt = []string{uniq[0].at, s.at}
			out.CreateUnread = fmt.Sprintf("its Create sets two different ids, %s and %s", renderPieces(first.pieces), renderPieces(s.value.pieces))
			return
		}
	}
	out.CreateID = renderPieces(first.pieces)
	raw, short, located := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, pc := range first.pieces {
		if pc.Attr == "" {
			continue
		}
		if pc.Via == "get" || pc.Via == "vars" {
			if _, cloud := cloudPlaceholders[pc.Attr]; cloud {
				raw[pc.Attr] = true
			}
		}
		if pc.Via == "location" {
			located[pc.Attr] = true
		}
		if pc.Short {
			short[pc.Attr] = true
		}
		if pc.Trim != "" {
			if out.CreateTrim == nil {
				out.CreateTrim = map[string]string{}
			}
			out.CreateTrim[pc.Attr] = pc.Trim
		}
	}
	out.CreateRaw, out.CreateShort, out.CreateLocated = sortedSet(raw), sortedSet(short), sortedSet(located)
}

func sortedSet(m map[string]bool) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func readImporter(out *HandwrittenID, res rval) {
	t, ok := res.fields["Importer"]
	if !ok {
		out.Importer = "none"
		return
	}
	imp := t.force()
	if imp.kind != kStruct {
		out.Importer = "unread"
		out.ImporterUnread = "its Importer is not read as a schema.ResourceImporter literal: " + imp.why
		return
	}
	st, ok := imp.fields["State"]
	if !ok {
		st, ok = imp.fields["StateContext"]
	}
	if !ok {
		out.Importer = "unread"
		out.ImporterUnread = "its ResourceImporter sets no State"
		return
	}
	if sel, ok := st.e.(*ast.SelectorExpr); ok && (sel.Sel.Name == "ImportStatePassthrough" || sel.Sel.Name == "ImportStatePassthroughContext") {
		out.Importer = "passthrough"
		return
	}
	fv := st.force()
	if fv.kind != kFunc {
		out.Importer = "unread"
		out.ImporterUnread = "its importer is not read as a function: " + fv.why
		return
	}
	fn := fv.fn
	out.Importer = "func"
	out.ImporterAt = fn.pkg.at(fn.body())
	out.ImporterHash = importerHash(fn)
	ast.Inspect(fn.body(), func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || fn.pkg.text(call.Fun) != "tpgresource.ParseImportId" || len(call.Args) == 0 {
			return true
		}
		if cl, ok := call.Args[0].(*ast.CompositeLit); ok {
			for _, el := range cl.Elts {
				if bl, ok := el.(*ast.BasicLit); ok {
					if s, err := strconv.Unquote(bl.Value); err == nil {
						out.ImporterFormats = append(out.ImporterFormats, s)
					}
				}
			}
		}
		return true
	})
	sites := setIDs(fn, nil, 2)
	for i, s := range sites {
		if s.value.kind != kPieces {
			out.ImporterUnread = fmt.Sprintf("it sets the id with %s, from %s", s.text, s.value.why)
			out.ImporterID = ""
			return
		}
		r := renderPieces(s.value.pieces)
		if i > 0 && r != out.ImporterID {
			out.ImporterUnread = fmt.Sprintf("it sets two different ids, %s and %s", out.ImporterID, r)
			out.ImporterID = ""
			return
		}
		out.ImporterID = r
	}
}

// importerHash fingerprints an importer and what it calls in its own
// package, one level down: a ruling made against it (rulings.go) names the
// fingerprint it read, so a provider release that changes the code fails
// the ruling instead of keeping it silently.
func importerHash(fn *funcVal) string {
	h := sha256.New()
	if fn.decl != nil {
		h.Write([]byte(fn.pkg.source(fn.decl)))
	} else {
		h.Write([]byte(fn.pkg.source(fn.lit)))
	}
	seen := map[string]bool{}
	var names []string
	add := func(name string) {
		if _, ok := fn.pkg.funcs[name]; ok && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	ast.Inspect(fn.body(), func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if id, ok := call.Fun.(*ast.Ident); ok {
				add(id.Name)
			}
		}
		return true
	})
	for s := fn.env; s != nil; s = s.parent {
		for _, k := range sortedKeys(s.binds) {
			if v := s.binds[k]; v.kind == kFunc && v.fn.decl != nil {
				add(v.fn.decl.Name.Name)
			}
		}
	}
	sort.Strings(names)
	for _, n := range names {
		h.Write([]byte(fn.pkg.source(fn.pkg.funcs[n])))
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}
