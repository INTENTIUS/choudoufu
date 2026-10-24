// Copyright (c) The OpenTofu Authors
// SPDX-License-Identifier: MPL-2.0
// Copyright (c) 2023 HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package main

import (
	"fmt"
	"go/ast"
	"go/printer"
	"reflect"
	"strconv"
	"strings"
	"unicode"
)

// GitHub issue #2201 item 1, ruled 2026-10-23: the id generator may trust
// the constructor arguments of azurerm's Create call as a source for an id
// it cannot map by segment name. The arguments are constants, and values
// parsed out of an `_id` argument.
//
// The rule, in one paragraph. A resource's Create builds the id it stores
// with New<IdType>(...) and sets it with d.SetId(id.ID()). That call states,
// for every value segment of the id, where azurerm gets the value:
//
//   - a configuration argument: d.Get("x"), or a field of the model
//     metadata.Decode fills (read through its tfschema tag);
//   - the provider's subscription (the client's Account.SubscriptionId);
//   - a constant: a literal, a package constant, or a conversion of one;
//   - a field of an id that argument parsed: parsed.ResourceGroupName where
//     `parsed, err := pkg.ParseXId(d.Get("x_id").(string))`, or parsed.ID()
//     for a whole id.
//
// [loader.createCallSources] reads exactly those, segment by segment, and
// gives up, with the reason, on anything else: a value read from a
// response, minted (uuid), looked up at apply, assigned in a loop, or
// reached through a helper this generator does not follow. It also gives up
// when Create stores an id other than the one it constructed (SetId of a
// value read back from the server), because the constructor's arguments
// are then not what azurerm stores. A row whose segment names the
// arguments already found is untouched: this runs only where
// [mapSegments] found none.

// createCall is entry.CreateCall: the Create-call trace a row's components
// were built from.
type createCall struct {
	// File is the checkout-relative path of the file holding the call.
	File string `json:"file"`
	// Constructor is the id constructor the call names, package.Function.
	Constructor string `json:"constructor"`
	// Sources is the source of each value segment, by the id's segment
	// name. A within source is a value read out of the argument's parsed id.
	Sources map[string]identitySource `json:"sources"`
}

// ruleCreateCall names a component taken from a Create call.
const ruleCreateCall = "create-call"

// value kinds the trace yields.
const (
	vArg      = "argument"
	vWithin   = "within"
	vWholeID  = "whole-id"
	vConst    = "constant"
	vProvider = "provider"
)

// tval is the source of one expression.
type tval struct {
	Kind     string
	Arg      string    // vArg, vWithin, vWholeID: the configuration argument
	Constant string    // vConst
	IDType   *idSource // vWithin, vWholeID: the id type the argument is parsed as
	Field    string    // vWithin: the field of that id
}

func (a tval) same(b tval) bool {
	return a.Kind == b.Kind && a.Arg == b.Arg && a.Constant == b.Constant && a.Field == b.Field &&
		((a.IDType == nil) == (b.IDType == nil)) &&
		(a.IDType == nil || (a.IDType.Package == b.IDType.Package && a.IDType.TypeName == b.IDType.TypeName))
}

// scope is the function a trace reads locals from.
type scope struct {
	l       *loader
	p       *pkg
	f       *ast.File
	assigns map[string][]ast.Expr
	// declared is a local's declared type (`var model XModel`), decoded the
	// locals metadata.Decode(&x) fills.
	declared map[string]ast.Expr
	decoded  map[string]bool
}

func newScope(l *loader, p *pkg, f *ast.File, root ast.Node) *scope {
	s := &scope{l: l, p: p, f: f, assigns: map[string][]ast.Expr{}, declared: map[string]ast.Expr{}, decoded: map[string]bool{}}
	ast.Inspect(root, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.AssignStmt:
			switch {
			case len(x.Lhs) == len(x.Rhs):
				for i, lhs := range x.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						s.assigns[id.Name] = append(s.assigns[id.Name], x.Rhs[i])
						s.declare(id.Name, x.Rhs[i])
					}
				}
			case len(x.Rhs) == 1:
				if id, ok := x.Lhs[0].(*ast.Ident); ok {
					s.assigns[id.Name] = append(s.assigns[id.Name], x.Rhs[0])
				}
			}
		case *ast.ValueSpec:
			for i, nm := range x.Names {
				if x.Type != nil {
					s.declared[nm.Name] = x.Type
				}
				if i < len(x.Values) {
					s.assigns[nm.Name] = append(s.assigns[nm.Name], x.Values[i])
					s.declare(nm.Name, x.Values[i])
				}
			}
		case *ast.CallExpr:
			if se, ok := x.Fun.(*ast.SelectorExpr); ok && se.Sel.Name == "Decode" && len(x.Args) == 1 {
				if u, ok := x.Args[0].(*ast.UnaryExpr); ok {
					if id, ok := u.X.(*ast.Ident); ok {
						s.decoded[id.Name] = true
					}
				}
			}
		}
		return true
	})
	return s
}

// declare records `x := T{}` / `x := &T{}` as x's type.
func (s *scope) declare(name string, rhs ast.Expr) {
	if u, ok := rhs.(*ast.UnaryExpr); ok {
		rhs = u.X
	}
	if cl, ok := rhs.(*ast.CompositeLit); ok && cl.Type != nil {
		s.declared[name] = cl.Type
	}
}

func (s *scope) text(e ast.Node) string {
	var b strings.Builder
	printer.Fprint(&b, s.l.fset, e)
	t := strings.Join(strings.Fields(b.String()), " ")
	if len(t) > 90 {
		t = t[:90] + "..."
	}
	return t
}

// trace is the source of an expression, or why it has none.
func (s *scope) trace(e ast.Expr, depth int) (tval, string) {
	if depth > 8 {
		return tval{}, "its source is too deeply nested to follow"
	}
	switch x := e.(type) {
	case *ast.ParenExpr:
		return s.trace(x.X, depth+1)
	case *ast.StarExpr:
		return s.trace(x.X, depth+1)
	case *ast.TypeAssertExpr:
		return s.trace(x.X, depth+1)
	case *ast.BasicLit:
		if v, ok := stringLit(x); ok {
			return tval{Kind: vConst, Constant: v}, ""
		}
		return tval{}, s.text(x) + " is not a string constant"
	case *ast.Ident:
		return s.traceIdent(x, depth)
	case *ast.SelectorExpr:
		return s.traceSelector(x, depth)
	case *ast.CallExpr:
		return s.traceCall(x, depth)
	}
	return tval{}, s.text(e) + " is not a form this generator traces"
}

func (s *scope) traceIdent(x *ast.Ident, depth int) (tval, string) {
	rhs := s.assigns[x.Name]
	if len(rhs) == 0 {
		if c, ok := s.p.consts[x.Name]; ok {
			return s.traceConst(s.p, s.f, c, depth)
		}
		return tval{}, x.Name + " is not assigned in the Create function"
	}
	var out tval
	for i, r := range rhs {
		v, why := s.trace(r, depth+1)
		if why != "" {
			return tval{}, x.Name + " is " + why
		}
		if i > 0 && !out.same(v) {
			return tval{}, x.Name + " is assigned from several sources"
		}
		out = v
	}
	return out, ""
}

// traceConst reads a package-level constant: a string literal, or a
// conversion of one (`RecordType("A")`).
func (s *scope) traceConst(p *pkg, f *ast.File, e ast.Expr, depth int) (tval, string) {
	if depth > 8 {
		return tval{}, "its constant is too deeply nested"
	}
	switch x := e.(type) {
	case *ast.BasicLit:
		if v, ok := stringLit(x); ok {
			return tval{Kind: vConst, Constant: v}, ""
		}
	case *ast.ParenExpr:
		return s.traceConst(p, f, x.X, depth+1)
	case *ast.CallExpr:
		if len(x.Args) == 1 {
			return s.traceConst(p, f, x.Args[0], depth+1)
		}
	case *ast.Ident:
		if c, ok := p.consts[x.Name]; ok {
			return s.traceConst(p, f, c, depth+1)
		}
	}
	return tval{}, "a constant that is not a string literal"
}

func (s *scope) traceSelector(x *ast.SelectorExpr, depth int) (tval, string) {
	// The provider's subscription: <client>.Account.SubscriptionId.
	if inner, ok := x.X.(*ast.SelectorExpr); ok && inner.Sel.Name == "Account" &&
		(x.Sel.Name == "SubscriptionId" || x.Sel.Name == "SubscriptionID") {
		return tval{Kind: vProvider}, ""
	}
	id, ok := x.X.(*ast.Ident)
	if !ok {
		return tval{}, s.text(x) + " is not a field of a local"
	}
	// pkg.Const.
	if ip, isPkg := s.p.imports[s.f][id.Name]; isPkg && len(s.assigns[id.Name]) == 0 {
		op := s.l.load(ip)
		if op == nil {
			return tval{}, s.text(x) + " is in a package this generator cannot read"
		}
		c, ok := op.consts[x.Sel.Name]
		if !ok {
			return tval{}, s.text(x) + " is not a constant it can read"
		}
		return s.traceConst(op, nil, c, depth+1)
	}
	// A field of the decoded model.
	if s.decoded[id.Name] {
		attr, why := s.modelField(id.Name, x.Sel.Name)
		if why != "" {
			return tval{}, why
		}
		return tval{Kind: vArg, Arg: attr}, ""
	}
	// A field of a parsed id.
	if parsed, why := s.parsedID(id.Name, depth); parsed != nil {
		return tval{Kind: vWithin, Arg: parsed.arg, IDType: parsed.idType, Field: x.Sel.Name}, ""
	} else if why != "" {
		return tval{}, why
	}
	return tval{}, s.text(x) + " is not read from the configuration"
}

// modelField is the argument a field of the decoded model carries: its
// tfschema tag.
func (s *scope) modelField(local, field string) (string, string) {
	te := s.declared[local]
	if te == nil {
		return "", local + " is decoded but its type is not declared in the Create function"
	}
	tp, tname := s.l.typeRef(s.p, s.f, te)
	if tp == nil {
		return "", "the type of " + local + " is not found"
	}
	ts := tp.types[tname]
	st, ok := ts.Type.(*ast.StructType)
	if !ok {
		return "", tname + " is not a struct"
	}
	for _, fld := range st.Fields.List {
		for _, nm := range fld.Names {
			if nm.Name != field {
				continue
			}
			if fld.Tag == nil {
				return "", tname + "." + field + " has no tag"
			}
			tag, err := strconv.Unquote(fld.Tag.Value)
			if err != nil {
				return "", tname + "." + field + " has an unreadable tag"
			}
			if v := reflect.StructTag(tag).Get("tfschema"); v != "" && v != "-" {
				return v, ""
			}
			return "", tname + "." + field + " has no tfschema tag"
		}
	}
	return "", tname + " has no field " + field
}

type parsedIDInfo struct {
	arg    string
	idType *idSource
}

// parsedID says whether local is `local, err := pkg.ParseXId(<argument>)`
// and of which id type. The first return is nil when it is not such a
// local; why is set when it is one that cannot be followed.
func (s *scope) parsedID(local string, depth int) (*parsedIDInfo, string) {
	rhs := s.assigns[local]
	if len(rhs) == 0 {
		return nil, ""
	}
	var out *parsedIDInfo
	for _, r := range rhs {
		call, ok := r.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return nil, ""
		}
		fp, ff, fd := s.l.funcRef(s.p, s.f, call.Fun)
		if fd == nil || fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
			return nil, ""
		}
		tp, tname := s.l.typeRef(fp, ff, fd.Type.Results.List[0].Type)
		if tp == nil || !s.l.isIDType(tp, tname) {
			return nil, ""
		}
		idSrc, problem := s.l.segmentsOf(tp, tname)
		if problem != "" || idSrc.Segments == nil {
			return nil, local + " is parsed as " + tname + ", which states no segments this generator reads"
		}
		v, why := s.trace(call.Args[0], depth+1)
		if why != "" {
			return nil, local + " is parsed from " + why
		}
		if v.Kind != vArg {
			return nil, local + " is parsed from " + s.text(call.Args[0]) + ", which is not a configuration argument"
		}
		cur := &parsedIDInfo{arg: v.Arg, idType: idSrc}
		if out != nil && (out.arg != cur.arg || out.idType.TypeName != cur.idType.TypeName || out.idType.Package != cur.idType.Package) {
			return nil, local + " is parsed several ways"
		}
		out = cur
	}
	return out, ""
}

func (s *scope) traceCall(x *ast.CallExpr, depth int) (tval, string) {
	fs := s.text(x.Fun)
	switch {
	case strings.Contains(fs, "uuid") || strings.Contains(fs, "GenerateUUID") || strings.Contains(fs, "rand."):
		return tval{}, "minted at apply (" + fs + ")"
	}
	if se, ok := x.Fun.(*ast.SelectorExpr); ok {
		switch se.Sel.Name {
		case "Get":
			if len(x.Args) == 1 {
				if key, ok := stringLit(x.Args[0]); ok {
					return tval{Kind: vArg, Arg: key}, ""
				}
			}
		case "ID":
			// parsed.ID(): the whole id the argument held.
			if len(x.Args) == 0 {
				if id, ok := se.X.(*ast.Ident); ok {
					if parsed, why := s.parsedID(id.Name, depth); parsed != nil {
						return tval{Kind: vWholeID, Arg: parsed.arg, IDType: parsed.idType}, ""
					} else if why != "" {
						return tval{}, why
					}
				}
			}
		}
	}
	// A conversion of one value: string(x), T(x).
	if len(x.Args) == 1 {
		isConv := false
		if id, ok := x.Fun.(*ast.Ident); ok && id.Name == "string" {
			isConv = true
		} else if tp, _ := s.l.typeRef(s.p, s.f, x.Fun); tp != nil {
			isConv = true
		}
		if isConv {
			return s.trace(x.Args[0], depth+1)
		}
	}
	return tval{}, "the result of " + fs + "(...), which no configuration states"
}

// createFound is one id constructor call in a Create function, with the
// scope its arguments are read in.
type createFound struct {
	call    *ast.CallExpr
	sc      *scope
	ctor    *ast.FuncDecl
	ctorPkg *pkg
	root    ast.Node
}

// createCallSources reads the constructor call Create builds r's id with,
// for an id of segments segs, and the source of every value segment.
func (l *loader) createCallSources(r registration, segs []segment) (*createCall, map[int]tval, string) {
	if r.Create == nil || r.CreatePkg == nil {
		return nil, nil, "no Create function found in source"
	}
	if segs == nil {
		return nil, nil, "the id states no segments this generator reads"
	}
	shape := templateShape(template(segs))
	var found []createFound
	l.findCtorCalls(r.CreatePkg, r.CreateFile, r.Create, shape, 0, map[ast.Node]bool{}, &found)
	if len(found) == 0 {
		return nil, nil, "Create does not build the id with a constructor of an id shaped " + template(segs)
	}
	var vals map[int]tval
	var first createFound
	for i, c := range found {
		v, why := c.sc.constructorValues(c, segs)
		if why != "" {
			return nil, nil, why
		}
		if i == 0 {
			vals, first = v, c
			continue
		}
		for k, a := range v {
			if !a.same(vals[k]) {
				return nil, nil, "Create builds the id in more than one way"
			}
		}
	}
	// The stored id must be the constructed one.
	for _, c := range found {
		if why := c.sc.setIDsAreConstructed(c.root, found); why != "" {
			return nil, nil, why
		}
	}
	sc := &createCall{
		File:        l.relPath(l.fset.Position(first.call.Pos()).Filename),
		Constructor: first.sc.text(first.call.Fun),
		Sources:     map[string]identitySource{},
	}
	for i, v := range vals {
		sc.Sources[segs[i].Name] = v.source(segs[i])
	}
	return sc, vals, ""
}

func (v tval) source(seg segment) identitySource {
	src := identitySource{Segment: seg.Name}
	switch v.Kind {
	case vArg, vWholeID:
		src.Argument = v.Arg
	case vProvider:
		src.Provider = cloudSubscription
	case vConst:
		src.Constant = v.Constant
	case vWithin:
		src.Within = v.Arg
		src.ParentIDType = v.IDType.Package + "." + v.IDType.TypeName
		src.ParentTemplate = template(v.IDType.Segments)
		src.ParentSegment = lowerFirst(v.Field)
	}
	return src
}

// findCtorCalls collects the calls to an id constructor whose id has the
// given shape, in body and the same-module functions it calls, two deep.
func (l *loader) findCtorCalls(p *pkg, f *ast.File, body ast.Node, shape string, depth int, seen map[ast.Node]bool, out *[]createFound) {
	if p == nil || body == nil || seen[body] {
		return
	}
	seen[body] = true
	var calls []*ast.CallExpr
	var sc *scope
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		calls = append(calls, call)
		fp, ff, fd := l.funcRef(p, f, call.Fun)
		if fd == nil || !strings.HasPrefix(fd.Name.Name, "New") || fd.Type.Results == nil || len(fd.Type.Results.List) == 0 {
			return true
		}
		tp, tname := l.typeRef(fp, ff, fd.Type.Results.List[0].Type)
		if tp == nil || !l.isIDType(tp, tname) {
			return true
		}
		src, problem := l.segmentsOf(tp, tname)
		if problem != "" || src.Segments == nil || templateShape(template(src.Segments)) != shape {
			return true
		}
		if sc == nil {
			sc = newScope(l, p, f, body)
		}
		*out = append(*out, createFound{call: call, sc: sc, ctor: fd, ctorPkg: fp, root: body})
		return true
	})
	if len(*out) > 0 || depth >= 2 {
		return
	}
	for _, c := range calls {
		fp, ff, fd := l.funcRef(p, f, c.Fun)
		if fd == nil {
			if se, ok := c.Fun.(*ast.SelectorExpr); ok {
				fp, fd = p, p.uniqueMethod(se.Sel.Name)
				if fd != nil {
					ff = p.fileOf[fd]
				}
			}
		}
		if fd == nil || fd.Body == nil || !strings.HasPrefix(fp.path, l.module+"/internal/services/") {
			continue
		}
		l.findCtorCalls(fp, ff, fd.Body, shape, depth+1, seen, out)
	}
}

// constructorValues maps each argument of the constructor call to the
// value segment it fills (by the field the constructor assigns it to) and
// traces it.
func (sc *scope) constructorValues(c createFound, segs []segment) (map[int]tval, string) {
	var params []string
	for _, fld := range c.ctor.Type.Params.List {
		for _, nm := range fld.Names {
			params = append(params, nm.Name)
		}
	}
	if len(params) != len(c.call.Args) || (c.ctor.Type.Params.List != nil && isVariadic(c.ctor)) {
		return nil, "the id constructor " + c.ctor.Name.Name + " takes " + strconv.Itoa(len(params)) + " arguments and Create passes " + strconv.Itoa(len(c.call.Args))
	}
	fieldOf, why := constructorFields(c.ctor)
	if why != "" {
		return nil, why
	}
	out := map[int]tval{}
	for i, arg := range c.call.Args {
		field := fieldOf[params[i]]
		if field == "" {
			return nil, "the id constructor " + c.ctor.Name.Name + " does not store its argument " + params[i] + " in a field"
		}
		at := -1
		for j, s := range segs {
			if !fixedSegment(s) && strings.EqualFold(s.Name, field) {
				at = j
			}
		}
		if at < 0 {
			return nil, "the constructor's field " + field + " is no value segment of " + template(segs)
		}
		v, why := sc.trace(arg, 0)
		if why != "" {
			return nil, "segment {" + segs[at].Name + "} is " + why
		}
		out[at] = v
	}
	for j, s := range segs {
		if !fixedSegment(s) {
			if _, ok := out[j]; !ok {
				return nil, "the constructor call gives no value for segment {" + s.Name + "}"
			}
		}
	}
	return out, ""
}

func isVariadic(fd *ast.FuncDecl) bool {
	for _, f := range fd.Type.Params.List {
		if _, ok := f.Type.(*ast.Ellipsis); ok {
			return true
		}
	}
	return false
}

// constructorFields is the id field each parameter of a constructor is
// stored in: `return &Id{Field: param}` (a call wrapping the param is
// looked through).
func constructorFields(fd *ast.FuncDecl) (map[string]string, string) {
	out := map[string]string{}
	var lit *ast.CompositeLit
	ast.Inspect(fd.Body, func(n ast.Node) bool {
		if rs, ok := n.(*ast.ReturnStmt); ok && len(rs.Results) == 1 && lit == nil {
			e := rs.Results[0]
			if u, ok := e.(*ast.UnaryExpr); ok {
				e = u.X
			}
			lit, _ = e.(*ast.CompositeLit)
		}
		return lit == nil
	})
	if lit == nil {
		return nil, "the id constructor " + fd.Name.Name + " does not return a struct literal"
	}
	for _, el := range lit.Elts {
		kv, ok := el.(*ast.KeyValueExpr)
		if !ok {
			return nil, "the id constructor " + fd.Name.Name + " builds its struct without field names"
		}
		key, ok := kv.Key.(*ast.Ident)
		if !ok {
			continue
		}
		if p := firstIdent(kv.Value); p != "" {
			out[p] = key.Name
		}
	}
	return out, ""
}

func firstIdent(e ast.Expr) string {
	switch x := e.(type) {
	case *ast.Ident:
		return x.Name
	case *ast.CallExpr:
		for _, a := range x.Args {
			if n := firstIdent(a); n != "" {
				return n
			}
		}
	case *ast.StarExpr:
		return firstIdent(x.X)
	}
	return ""
}

// setIDsAreConstructed refuses a Create that stores an id other than the
// one it constructed: every SetId/SetID argument must be the empty string
// or the ID() of a local the constructor call is assigned to.
func (sc *scope) setIDsAreConstructed(root ast.Node, found []createFound) string {
	isCtor := func(e ast.Expr) bool {
		for _, f := range found {
			if e == ast.Expr(f.call) {
				return true
			}
		}
		return false
	}
	why := ""
	ast.Inspect(root, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || why != "" {
			return true
		}
		se, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || (se.Sel.Name != "SetId" && se.Sel.Name != "SetID") || len(call.Args) != 1 {
			return true
		}
		arg := call.Args[0]
		if s, ok := stringLit(arg); ok && s == "" {
			return true
		}
		// metadata.SetID(id) passes the id, d.SetId(id.ID()) its string.
		if c, ok := arg.(*ast.CallExpr); ok && len(c.Args) == 0 {
			if cs, ok := c.Fun.(*ast.SelectorExpr); ok && cs.Sel.Name == "ID" {
				arg = cs.X
			}
		}
		id, ok := arg.(*ast.Ident)
		if !ok {
			why = "Create stores " + sc.text(call.Args[0]) + " as the id, not the one the constructor builds"
			return true
		}
		rhs := sc.assigns[id.Name]
		if len(rhs) == 0 {
			why = "Create stores " + sc.text(call.Args[0]) + " as the id, which is not assigned from the constructor"
			return true
		}
		for _, r := range rhs {
			if !isCtor(r) {
				why = "Create stores " + sc.text(call.Args[0]) + " as the id, and " + id.Name + " is assigned from " + sc.text(r) + ", not the constructor"
				return true
			}
		}
		return true
	})
	return why
}

func upperFirst(s string) string {
	if s == "" {
		return s
	}
	r := []rune(s)
	r[0] = unicode.ToUpper(r[0])
	return string(r)
}

// createComponents is the template the Create call's sources make of an id
// segs that [mapSegments] could not map by name: each value segment read
// from where the constructor's argument says its value comes from. A
// leading run of segments parsed out of one `_id` argument is that
// argument's whole ARM id, when the id it is parsed as is that run and no
// more; a provider-subscription segment inside the run stands for the
// parent's own (azurerm builds the id in the provider's subscription, and
// the parent it parsed is in it). The row keeps its reason, with the
// problem recorded beside it, when any segment has no such source.
func createComponents(segs []segment, vals map[int]tval, ctx mapCtx) ([]component, string) {
	configurable := func(arg string) bool { return ctx.Schema.Attrs[arg].configurable() }
	lead := -1
	var leadArg string
	var leadType *idSource
	for i, s := range segs {
		if fixedSegment(s) {
			continue
		}
		v := vals[i]
		switch v.Kind {
		case vWithin:
			if leadArg == "" {
				leadArg, leadType = v.Arg, v.IDType
			}
			if v.Arg != leadArg || v.IDType.TypeName != leadType.TypeName || v.IDType.Package != leadType.Package {
				return nil, "its segments are parsed out of more than one argument's id (" + leadArg + ", " + v.Arg + ")"
			}
			lead = i
		case vArg:
			if !configurable(v.Arg) {
				return nil, fmt.Sprintf("segment {%s} is argument %s, which a configuration does not set", s.Name, v.Arg)
			}
			if ctx.ArgForms[v.Arg] == "arm-id" {
				return nil, fmt.Sprintf("segment {%s} is argument %s, which holds a whole ARM id", s.Name, v.Arg)
			}
			if s.Kind == segScope || s.Kind == segResourceID {
				return nil, fmt.Sprintf("segment {%s} spans several path segments and argument %s is one value", s.Name, v.Arg)
			}
		case vWholeID:
			if !configurable(v.Arg) {
				return nil, fmt.Sprintf("segment {%s} is argument %s, which a configuration does not set", s.Name, v.Arg)
			}
			if s.Kind != segScope && s.Kind != segResourceID {
				return nil, fmt.Sprintf("segment {%s} is one value and the Create call passes the whole id of argument %s", s.Name, v.Arg)
			}
		case vProvider:
			if s.Kind != segSubscription {
				return nil, fmt.Sprintf("segment {%s} is the provider's subscription and is not a subscription segment", s.Name)
			}
		case vConst:
			if s.Kind == segScope || s.Kind == segResourceID || s.Kind == segDataPlane {
				return nil, fmt.Sprintf("segment {%s} spans several path segments and the Create call gives it the constant %q", s.Name, v.Constant)
			}
			if len(s.Values) > 0 && !containsFold(s.Values, v.Constant) {
				return nil, fmt.Sprintf("segment {%s} is the constant %q, which is not one of its possible values", s.Name, v.Constant)
			}
		default:
			return nil, fmt.Sprintf("segment {%s} has no source", s.Name)
		}
	}
	for _, s := range segs {
		if s.Kind == segDataPlane {
			return nil, "data-plane id (a URL, not an ARM path)"
		}
	}
	var out []component
	start := 0
	if lead >= 0 {
		parent := leadType.Segments
		if lead >= len(parent) {
			return nil, "the id parsed from " + leadArg + " is shorter than the leading run it supplies"
		}
		for j := 0; j <= lead; j++ {
			s := segs[j]
			if fixedSegment(s) {
				continue
			}
			v := vals[j]
			switch v.Kind {
			case vWithin:
				at := -1
				for k, ps := range parent {
					if !fixedSegment(ps) && strings.EqualFold(ps.Name, v.Field) {
						at = k
					}
				}
				if at != j {
					return nil, fmt.Sprintf("segment {%s} is field %s of %s's id, which is not at the same position in that id", s.Name, v.Field, leadArg)
				}
			case vProvider:
				if parent[j].Kind != segSubscription {
					return nil, fmt.Sprintf("segment {%s} is the provider's subscription and %s's id has no subscription there", s.Name, leadArg)
				}
			default:
				return nil, fmt.Sprintf("segment {%s} comes from elsewhere than %s, whose id supplies the segments around it", s.Name, leadArg)
			}
		}
		if templateShape(template(segs[:lead+1])) != templateShape(template(parent)) {
			return nil, fmt.Sprintf("the id parsed from %s (%s) is not the leading run %s", leadArg, template(parent), template(segs[:lead+1]))
		}
		if lead == len(segs)-1 {
			return nil, "the id is the whole of argument " + leadArg + "'s id (a parent-id settings object, GitHub issue #2201 item 2)"
		}
		out = append(out, component{Attrs: []string{leadArg}, ARMID: true, Segment: segs[lead].Name, Rule: ruleCreateCall})
		start = lead + 1
	}
	for i := start; i < len(segs); i++ {
		s := segs[i]
		if fixedSegment(s) {
			out = appendLiteral(out, "/"+s.Value)
			continue
		}
		v := vals[i]
		switch v.Kind {
		case vArg:
			out = appendLiteral(out, "/")
			out = append(out, component{Attrs: []string{v.Arg}, Segment: s.Name, Rule: ruleCreateCall})
		case vWholeID:
			out = append(out, component{Attrs: []string{v.Arg}, ARMID: true, Segment: s.Name, Rule: ruleCreateCall})
		case vProvider:
			out = appendLiteral(out, "/")
			out = append(out, component{Cloud: cloudSubscription, Segment: s.Name, Rule: ruleSubscription})
		case vConst:
			out = appendLiteral(out, "/"+v.Constant)
		case vWithin:
			return nil, fmt.Sprintf("segment {%s} is parsed out of %s's id after the id began", s.Name, v.Arg)
		}
	}
	return out, ""
}

func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}

// sameComponents reports whether two component lists render the same id
// from the same arguments, ignoring which rule chose them.
func sameComponents(a, b []component) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Literal != b[i].Literal || a[i].Cloud != b[i].Cloud || a[i].ARMID != b[i].ARMID || !reflect.DeepEqual(a[i].Attrs, b[i].Attrs) {
			return false
		}
	}
	return true
}
