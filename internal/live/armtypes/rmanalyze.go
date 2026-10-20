// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package armtypes

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"
)

// The create-path analysis behind the after-create column of the azurerm
// tag facts. For one resource it walks the Create function in source
// order, following calls into the provider's own packages (the update
// function a create returns into, the expand helpers that build a
// payload), and tracks which values carry the configuration's tags:
//
//   - a read of the argument: X.Get("tags") or X.GetOk("tags") on the
//     ResourceData (untyped SDK), or <model>.<field> where the model was
//     filled by metadata.Decode(&model) and the field is declared
//     `tfschema:"tags"` (typed SDK);
//   - anything built from such a value: a composite literal with it in a
//     field, a local assigned from it, a struct whose field is assigned it,
//     a helper's result when the helper returns it.
//
// Every call into code outside the provider (the go-azure-sdk clients, the
// data-plane clients) whose first argument is a context is an API call.
// The first API call whose name says it creates is the create call. If it
// carries tags, they ride the create (in-create). If it does not and a
// later API call does, that later call writes them after the create
// returns (after-create). Anything else is unknown, with the reason.
//
// It is a flow-insensitive, single-pass taint walk: a value once tainted
// stays tainted, and a loop is walked once. That over-approximates
// "carries tags", which can only move a resource toward in-create; the
// committed table's known rows (rmtags_test.go) pin the classes the rule
// must reproduce.

const rmMaxDepth = 6

type rmCall struct {
	text    string // the callee as written: client.UpdateTags
	name    string // the method name: UpdateTags
	pos     string // file:line in the checkout
	tainted bool   // an argument carries the configuration's tags
	// cond is set when the call sits under an if, switch or select of
	// the walk's entry function (or of the call site that inlined it): a
	// path some runs take and others skip.
	cond bool
}

type rmFrame struct {
	p      *rmPkg
	f      *ast.File
	taint  map[string]bool
	models map[string]bool
	// locals are the names the function declares, which shadow an
	// import of the same name (a local `client` beside go-azure-sdk's
	// sdk/client package).
	locals map[string]bool
	// recvName and recvType are the method's receiver, when it is one.
	recvName, recvType string
	params             []string // in declaration order, for the summary
	// returns and paramTaint are the summary handed back to a caller.
	returns    bool
	paramTaint map[string]bool
}

type rmAnalyzer struct {
	l        *rmLoader
	stack    map[*ast.FuncDecl]bool
	depth    int
	events   []rmCall
	tagReads int
	budget   int
	cond     int
	// exhausted is set when the inlining budget ran out, which makes the
	// resource's answer unknown rather than a guess from a partial walk.
	exhausted bool
}

func newRMAnalyzer(l *rmLoader) *rmAnalyzer {
	return &rmAnalyzer{l: l, stack: map[*ast.FuncDecl]bool{}, budget: 20000}
}

// analyzeBody walks one function body as the entry point. outer is the
// method the body belongs to, or encloses it as a function literal (a
// typed resource's Create()), for its receiver.
func (a *rmAnalyzer) analyzeBody(p *rmPkg, f *ast.File, outer *ast.FuncDecl, ft *ast.FuncType, body *ast.BlockStmt) {
	fr := newRMFrame(p, f)
	fr.recvName, fr.recvType = receiverOf(outer)
	fr.locals[fr.recvName] = true
	a.declareParams(fr, ft)
	a.walk(fr, body)
}

// call inlines a provider function: its parameters inherit the taint and
// model-ness of the arguments, its events are appended in place, and the
// caller learns whether the result carries tags and which pointer
// arguments it tainted.
func (a *rmAnalyzer) call(p *rmPkg, f *ast.File, fd *ast.FuncDecl, args []ast.Expr, argT []bool, argModel []bool) (returns bool, tainted []bool) {
	if a.budget <= 0 {
		a.exhausted = true
	}
	if fd.Body == nil || a.stack[fd] || a.depth >= rmMaxDepth || a.budget <= 0 {
		return anyTrue(argT), nil
	}
	a.budget--
	a.stack[fd] = true
	a.depth++
	defer func() { a.depth--; delete(a.stack, fd) }()

	fr := newRMFrame(p, f)
	fr.recvName, fr.recvType = receiverOf(fd)
	fr.locals[fr.recvName] = true
	a.declareParams(fr, fd.Type)
	i := 0
	for _, fld := range fd.Type.Params.List {
		names := fld.Names
		if len(names) == 0 {
			fr.params = append(fr.params, "") // keeps positions aligned with args
			i++
			continue
		}
		for _, n := range names {
			fr.params = append(fr.params, n.Name)
			if i < len(argT) && argT[i] {
				fr.taint[n.Name] = true
			}
			if i < len(argModel) && argModel[i] {
				fr.models[n.Name] = true
			}
			i++
		}
	}
	a.walk(fr, fd.Body)
	tainted = make([]bool, len(args))
	for j, n := range fr.params {
		if j < len(tainted) && n != "" && fr.paramTaint[n] {
			tainted[j] = true
		}
	}
	return fr.returns, tainted
}

func newRMFrame(p *rmPkg, f *ast.File) *rmFrame {
	return &rmFrame{p: p, f: f, taint: map[string]bool{}, models: map[string]bool{}, locals: map[string]bool{}, paramTaint: map[string]bool{}}
}

func (a *rmAnalyzer) declareParams(fr *rmFrame, ft *ast.FuncType) {
	if ft == nil {
		return
	}
	for _, fl := range []*ast.FieldList{ft.Params, ft.Results} {
		if fl == nil {
			continue
		}
		for _, fld := range fl.List {
			for _, n := range fld.Names {
				fr.locals[n.Name] = true
			}
		}
	}
}

func anyTrue(bs []bool) bool {
	for _, b := range bs {
		if b {
			return true
		}
	}
	return false
}

func (a *rmAnalyzer) walk(fr *rmFrame, n ast.Node) {
	switch s := n.(type) {
	case nil:
		return
	case *ast.AssignStmt:
		if s.Tok == token.DEFINE {
			for _, lhs := range s.Lhs {
				if id, ok := lhs.(*ast.Ident); ok {
					fr.locals[id.Name] = true
				}
			}
		}
		rt := make([]bool, len(s.Rhs))
		for i, r := range s.Rhs {
			rt[i] = a.eval(fr, r)
		}
		for i, lhs := range s.Lhs {
			t := false
			if len(s.Rhs) == len(s.Lhs) {
				t = rt[i]
			} else if len(s.Rhs) == 1 {
				t = rt[0]
			}
			if t {
				a.taintRoot(fr, lhs)
			}
		}
	case *ast.ValueSpec:
		for _, n := range s.Names {
			fr.locals[n.Name] = true
		}
		for i, v := range s.Values {
			if a.eval(fr, v) {
				if i < len(s.Names) && len(s.Values) == len(s.Names) {
					fr.taint[s.Names[i].Name] = true
				} else {
					for _, n := range s.Names {
						fr.taint[n.Name] = true
					}
				}
			}
		}
	case *ast.ReturnStmt:
		for _, r := range s.Results {
			if a.eval(fr, r) {
				fr.returns = true
			}
		}
	case *ast.RangeStmt:
		if s.Tok == token.DEFINE {
			for _, e := range []ast.Expr{s.Key, s.Value} {
				if id, ok := e.(*ast.Ident); ok {
					fr.locals[id.Name] = true
				}
			}
		}
		if a.eval(fr, s.X) {
			a.taintRoot(fr, s.Key)
			a.taintRoot(fr, s.Value)
		}
		a.walk(fr, s.Body)
	case *ast.IfStmt:
		a.walk(fr, s.Init)
		a.eval(fr, s.Cond)
		a.cond++
		a.walk(fr, s.Body)
		a.walk(fr, s.Else)
		a.cond--
	case *ast.SwitchStmt:
		a.walk(fr, s.Init)
		a.eval(fr, s.Tag)
		a.cond++
		a.walk(fr, s.Body)
		a.cond--
	case *ast.TypeSwitchStmt:
		a.walk(fr, s.Init)
		a.walk(fr, s.Assign)
		a.cond++
		a.walk(fr, s.Body)
		a.cond--
	case *ast.SelectStmt:
		a.cond++
		a.walk(fr, s.Body)
		a.cond--
	case ast.Expr:
		a.eval(fr, s)
	default:
		ast.Inspect(n, func(c ast.Node) bool {
			if c == n {
				return true
			}
			if c != nil {
				a.walk(fr, c)
			}
			return false
		})
	}
}

// taintRoot marks the variable at the root of an assignment target:
// payload in payload.Properties.Tags = t, x in *x = t.
func (a *rmAnalyzer) taintRoot(fr *rmFrame, e ast.Expr) {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			if x.Name == "_" {
				return
			}
			fr.taint[x.Name] = true
			for _, p := range fr.params {
				if p == x.Name {
					fr.paramTaint[p] = true
				}
			}
			return
		case *ast.SelectorExpr:
			e = x.X
		case *ast.IndexExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.UnaryExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return
		}
	}
}

func (a *rmAnalyzer) isImport(fr *rmFrame, id *ast.Ident) bool {
	if fr.locals[id.Name] || fr.taint[id.Name] || fr.models[id.Name] {
		return false
	}
	_, ok := fr.p.imports[fr.f][id.Name]
	return ok
}

// eval walks an expression and reports whether its value carries tags.
func (a *rmAnalyzer) eval(fr *rmFrame, e ast.Expr) bool {
	switch x := e.(type) {
	case nil:
		return false
	case *ast.Ident:
		return fr.taint[x.Name]
	case *ast.BasicLit:
		return false
	case *ast.FuncLit:
		// A closure (a retry or poll function, say) runs where it is
		// written as far as this walk is concerned.
		a.declareParams(fr, x.Type)
		a.walk(fr, x.Body)
		return false
	case *ast.CallExpr:
		return a.evalCall(fr, x)
	case *ast.SelectorExpr:
		if id, ok := x.X.(*ast.Ident); ok {
			if fr.models[id.Name] && a.isTagField(x.Sel.Name) {
				a.tagReads++
				return true
			}
			if a.isImport(fr, id) {
				return false
			}
		}
		return a.eval(fr, x.X)
	case *ast.CompositeLit:
		t := false
		for _, el := range x.Elts {
			if a.eval(fr, el) {
				t = true
			}
		}
		return t
	case *ast.KeyValueExpr:
		if _, ok := x.Key.(*ast.Ident); !ok {
			a.eval(fr, x.Key)
		}
		return a.eval(fr, x.Value)
	case *ast.UnaryExpr:
		return a.eval(fr, x.X)
	case *ast.StarExpr:
		return a.eval(fr, x.X)
	case *ast.ParenExpr:
		return a.eval(fr, x.X)
	case *ast.TypeAssertExpr:
		return a.eval(fr, x.X)
	case *ast.IndexExpr:
		t := a.eval(fr, x.X)
		a.eval(fr, x.Index)
		return t
	case *ast.SliceExpr:
		return a.eval(fr, x.X)
	case *ast.BinaryExpr:
		l := a.eval(fr, x.X)
		r := a.eval(fr, x.Y)
		return l || r
	}
	return false
}

func (a *rmAnalyzer) isTagField(name string) bool {
	if name == "Tags" {
		return true
	}
	for _, p := range a.l.pkgs {
		if p != nil && p.tagFields[name] {
			return true
		}
	}
	return false
}

// isTagsGet matches X.Get("tags"), X.GetOk("tags") and their kin on a
// ResourceData, under whatever name the function gives it.
func isTagsGet(c *ast.CallExpr) bool {
	sel, ok := c.Fun.(*ast.SelectorExpr)
	if !ok || len(c.Args) == 0 {
		return false
	}
	switch sel.Sel.Name {
	case "Get", "GetOk", "GetOkExists", "GetChange", "GetRawConfig":
	default:
		return false
	}
	s, ok := rmStringLit(c.Args[0])
	return ok && s == "tags"
}

func (a *rmAnalyzer) evalCall(fr *rmFrame, c *ast.CallExpr) bool {
	if isTagsGet(c) {
		a.tagReads++
		return true
	}
	argT := make([]bool, len(c.Args))
	argModel := make([]bool, len(c.Args))
	for i, arg := range c.Args {
		argT[i] = a.eval(fr, arg)
		if id := rmRootIdent(arg); id != nil && fr.models[id.Name] {
			argModel[i] = true
		}
	}
	sel, isSel := c.Fun.(*ast.SelectorExpr)
	if isSel && sel.Sel.Name == "Decode" && len(c.Args) == 1 {
		if u, ok := c.Args[0].(*ast.UnaryExpr); ok && u.Op == token.AND {
			if id, ok := u.X.(*ast.Ident); ok {
				fr.models[id.Name] = true
			}
		}
		return false
	}

	// A function in the provider's own packages: inline it.
	if cp, cf, fd := a.resolve(fr, c.Fun); fd != nil {
		ret, pt := a.call(cp, cf, fd, c.Args, argT, argModel)
		for i, t := range pt {
			if t {
				a.taintRoot(fr, c.Args[i])
			}
		}
		return ret
	}
	if fl, ok := c.Fun.(*ast.FuncLit); ok {
		a.declareParams(fr, fl.Type)
		a.walk(fr, fl.Body)
		return anyTrue(argT)
	}

	recvT := false
	if isSel {
		if id, ok := sel.X.(*ast.Ident); !ok || !a.isImport(fr, id) {
			recvT = a.eval(fr, sel.X)
			if isCtxArg(c) {
				a.events = append(a.events, rmCall{
					text:    types.ExprString(c.Fun),
					name:    sel.Sel.Name,
					pos:     a.l.pos(c),
					tainted: anyTrue(argT),
					cond:    a.cond > 0,
				})
			}
		}
	}
	return anyTrue(argT) || (recvT && isSel && isBuilder(sel.Sel.Name))
}

// isBuilder names the receiver methods whose result is the receiver's
// data again (pointer.To-like accessors on a payload are functions, not
// methods; this keeps x.Copy() style calls carrying x's taint).
func isBuilder(name string) bool {
	return name == "Copy" || name == "DeepCopy" || name == "Clone"
}

// isCtxArg reports whether a call's first argument is a context: the
// shape of every go-azure-sdk and data-plane client call.
func isCtxArg(c *ast.CallExpr) bool {
	if len(c.Args) == 0 {
		return false
	}
	s := strings.ToLower(types.ExprString(c.Args[0]))
	return s == "ctx" || strings.HasSuffix(s, "ctx") || strings.HasPrefix(s, "context.")
}

func rmRootIdent(e ast.Expr) *ast.Ident {
	for {
		switch x := e.(type) {
		case *ast.Ident:
			return x
		case *ast.UnaryExpr:
			e = x.X
		case *ast.StarExpr:
			e = x.X
		case *ast.ParenExpr:
			e = x.X
		default:
			return nil
		}
	}
}

// resolve finds a call target among the provider's own declarations: a
// package function, a qualified function in another provider package, or
// a method called on the function's own receiver (a typed resource's
// r.mapModelToPayload). A method called on any other value is the SDK's
// or a data-plane client's, which is what an API call is.
func (a *rmAnalyzer) resolve(fr *rmFrame, fun ast.Expr) (*rmPkg, *ast.File, *ast.FuncDecl) {
	switch x := fun.(type) {
	case *ast.Ident:
		if fr.locals[x.Name] {
			return nil, nil, nil // a function-typed local, not a declaration
		}
	case *ast.SelectorExpr:
		id, ok := x.X.(*ast.Ident)
		if !ok {
			return nil, nil, nil
		}
		if fr.recvName != "" && id.Name == fr.recvName {
			mp, m := a.l.methodWithEmbedding(fr.p, fr.recvType, x.Sel.Name, 0)
			if m == nil || !a.l.inModule(mp) {
				return nil, nil, nil
			}
			return mp, mp.fileOf[m], m
		}
		if !a.isImport(fr, id) {
			return nil, nil, nil
		}
	default:
		return nil, nil, nil
	}
	if cp, cf, fd := a.l.funcRef(fr.p, fr.f, fun); fd != nil {
		if a.l.inModule(cp) && !strings.HasSuffix(cp.path, "/internal/tags") {
			return cp, cf, fd
		}
	}
	return nil, nil, nil
}

// receiverOf names a method's receiver and its type.
func receiverOf(fd *ast.FuncDecl) (name, typ string) {
	if fd == nil || fd.Recv == nil || len(fd.Recv.List) == 0 {
		return "", ""
	}
	r := fd.Recv.List[0]
	if len(r.Names) == 1 {
		name = r.Names[0].Name
	}
	return name, rmRecvTypeName(r.Type)
}

// rmCreateVerb reports whether an API call's name says it creates.
func rmCreateVerb(name string) bool {
	if strings.Contains(name, "Check") || strings.Contains(name, "Get") || strings.Contains(name, "List") {
		return false
	}
	// go-azure-sdk names an operation <Operation>[Callback][ThenPoll],
	// and operations of a sub-resource <Resource><Verb>
	// (ResourceGuardsPut, QueriesPut): the verb may sit anywhere.
	for _, v := range []string{"Create", "Put"} {
		if strings.Contains(name, v) {
			return true
		}
	}
	// Data-plane clients: Key Vault's SetSecret and ImportCertificate,
	// and the like.
	for _, p := range []string{"Set", "Import", "Add", "Upsert", "Register", "Provision"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}
